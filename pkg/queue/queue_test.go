// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package queue_test

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/bborbe/errors"
	"github.com/bborbe/run"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/queue"
)

// applyLog is a mutex-guarded record of which writes the consumer reached, and
// which of them returned. It is how a spec observes ordering without touching
// the queue's internals.
type applyLog struct {
	mu       sync.Mutex
	started  []string
	finished []string
}

func (l *applyLog) start(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.started = append(l.started, id)
}

func (l *applyLog) finish(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.finished = append(l.finished, id)
}

func (l *applyLog) startedIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.started...)
}

func (l *applyLog) finishedIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.finished...)
}

func (l *applyLog) hasStarted(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.started, id)
}

func (l *applyLog) hasFinished(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.finished, id)
}

// releaseSet hands out release channels and closes every one of them on
// cleanup, so no spec can leave an Apply blocked and Consume stuck in its wait.
type releaseSet struct {
	mu       sync.Mutex
	channels []chan struct{}
}

func (s *releaseSet) new() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel := make(chan struct{})
	s.channels = append(s.channels, channel)
	return channel
}

func (s *releaseSet) releaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, channel := range s.channels {
		closeOnce(channel)
	}
}

// closeOnce closes channel unless it is already closed.
func closeOnce(channel chan struct{}) {
	select {
	case <-channel:
	default:
		close(channel)
	}
}

// recordingApply applies at once, recording the write before and after.
func recordingApply(log *applyLog, id string) run.Func {
	return func(ctx context.Context) error {
		log.start(id)
		log.finish(id)
		return nil
	}
}

// blockingApply records the write as started, then blocks until release is
// closed or ctx is cancelled, then records it as finished.
func blockingApply(log *applyLog, id string, release chan struct{}) run.Func {
	return func(ctx context.Context) error {
		log.start(id)
		select {
		case <-release:
		case <-ctx.Done():
		}
		log.finish(id)
		return nil
	}
}

// stubbornApply blocks on release alone, deliberately ignoring ctx, so a spec
// can observe Consume waiting for an in-flight Apply that outlives its context.
func stubbornApply(log *applyLog, id string, release chan struct{}) run.Func {
	return func(ctx context.Context) error {
		log.start(id)
		<-release
		log.finish(id)
		return nil
	}
}

var _ = Describe("Queue", func() {
	Context("while consuming", func() {
		var (
			subject  queue.Queue
			ctx      context.Context
			log      *applyLog
			releases *releaseSet
		)

		BeforeEach(func() {
			subject = queue.NewQueue()
			log = &applyLog{}
			releases = &releaseSet{}

			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			consumed := make(chan error, 1)
			go func() {
				consumed <- subject.Consume(ctx)
			}()

			// Registered before the release cleanup so it runs after it
			// (DeferCleanup is LIFO): every Apply has already returned by the
			// time Consume's context is cancelled.
			DeferCleanup(func() {
				cancel()
				Eventually(consumed, "5s").Should(Receive(BeNil()))
			})
			DeferCleanup(releases.releaseAll)
		})

		enqueue := func(write queue.Write) {
			GinkgoHelper()
			Expect(subject.Enqueue(ctx, write)).To(Succeed())
		}

		It("applies one vault's writes in submission order", func() {
			release := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: blockingApply(log, "A", release)})
			enqueue(queue.Write{Vault: "alpha", ItemID: "B", Apply: recordingApply(log, "B")})

			Consistently(log.startedIDs, "200ms").ShouldNot(ContainElement("B"))
			Expect(log.startedIDs()).To(Equal([]string{"A"}))

			closeOnce(release)
			Eventually(log.startedIDs).Should(Equal([]string{"A", "B"}))
		})

		It("does not hold one vault behind another's backlog", func() {
			release := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: blockingApply(log, "A", release)})
			Eventually(log.startedIDs).Should(ContainElement("A"))

			enqueue(queue.Write{Vault: "beta", ItemID: "C", Apply: recordingApply(log, "C")})
			Eventually(log.startedIDs).Should(ContainElement("C"))

			Expect(log.hasFinished("A")).To(BeFalse())

			closeOnce(release)
			Eventually(log.finishedIDs).Should(ContainElement("A"))
		})

		It("returns from Enqueue without waiting for the write", func() {
			release := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: blockingApply(log, "A", release)})
			Eventually(log.startedIDs).Should(ContainElement("A"))

			Consistently(log.finishedIDs, "200ms").ShouldNot(ContainElement("A"))
			Expect(subject.Done("alpha")).NotTo(BeClosed())
		})

		It("calls Apply with the consumer context, not the producer context", func() {
			observed := make(chan error, 1)
			cancelled, cancelEnqueue := context.WithCancel(context.Background())
			cancelEnqueue()

			Expect(subject.Enqueue(cancelled, queue.Write{
				Vault:  "alpha",
				ItemID: "A",
				Apply: func(ctx context.Context) error {
					observed <- ctx.Err()
					return nil
				},
			})).To(Succeed())

			Eventually(observed, "5s").Should(Receive(BeNil()))
		})

		It("keeps applying after a write fails", func() {
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: func(ctx context.Context) error {
				log.start("A")
				log.finish("A")
				return errors.New(ctx, "write failed")
			}})
			enqueue(queue.Write{Vault: "alpha", ItemID: "B", Apply: recordingApply(log, "B")})

			Eventually(log.startedIDs).Should(Equal([]string{"A", "B"}))
		})

		It("survives a write that panics", func() {
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: func(ctx context.Context) error {
				log.start("A")
				panic("write exploded")
			}})
			enqueue(queue.Write{Vault: "alpha", ItemID: "B", Apply: recordingApply(log, "B")})

			Eventually(log.startedIDs).Should(Equal([]string{"A", "B"}))
			Eventually(log.finishedIDs).Should(ContainElement("B"))
		})

		It("accepts an unbounded backlog and applies it in order", func() {
			const backlog = 1000

			release := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: blockingApply(log, "A", release)})
			Eventually(log.startedIDs).Should(ContainElement("A"))

			expected := []string{"A"}
			for i := 1; i <= backlog; i++ {
				id := fmt.Sprintf("%d", i)
				expected = append(expected, id)
				enqueue(queue.Write{Vault: "alpha", ItemID: id, Apply: recordingApply(log, id)})
			}

			closeOnce(release)
			Eventually(log.startedIDs, "10s").Should(HaveLen(backlog + 1))
			Expect(log.startedIDs()).To(Equal(expected))
		})

		It("reports the done state per vault", func() {
			Expect(subject.Done("unknown")).To(BeClosed())

			firstRelease := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "A", Apply: blockingApply(log, "A", firstRelease)})
			Eventually(log.startedIDs).Should(ContainElement("A"))

			busy := subject.Done("alpha")
			Expect(busy).NotTo(BeClosed())

			closeOnce(firstRelease)
			Eventually(busy).Should(BeClosed())

			secondRelease := releases.new()
			enqueue(queue.Write{Vault: "alpha", ItemID: "B", Apply: blockingApply(log, "B", secondRelease)})
			Eventually(log.startedIDs).Should(ContainElement("B"))

			fresh := subject.Done("alpha")
			Expect(fresh).NotTo(BeClosed())
			Expect(fresh).NotTo(BeIdenticalTo(busy))

			closeOnce(secondRelease)
			Eventually(fresh).Should(BeClosed())
		})

		It("rejects an empty vault and a nil Apply without applying anything", func() {
			Expect(subject.Enqueue(ctx, queue.Write{
				ItemID: "A",
				Apply:  recordingApply(log, "A"),
			})).To(HaveOccurred())
			Expect(subject.Enqueue(ctx, queue.Write{
				Vault:  "alpha",
				ItemID: "A",
			})).To(HaveOccurred())

			Consistently(log.startedIDs, "100ms").Should(BeEmpty())
		})
	})

	Context("before Consume starts", func() {
		var (
			subject  queue.Queue
			log      *applyLog
			releases *releaseSet
		)

		BeforeEach(func() {
			subject = queue.NewQueue()
			log = &applyLog{}
			releases = &releaseSet{}
			DeferCleanup(releases.releaseAll)
		})

		It("holds writes and applies them in order once it starts", func() {
			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "A", Apply: recordingApply(log, "A"),
			})).To(Succeed())
			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "B", Apply: recordingApply(log, "B"),
			})).To(Succeed())
			Expect(log.startedIDs()).To(BeEmpty())

			done := subject.Done("alpha")
			Expect(done).NotTo(BeClosed())

			ctx, cancel := context.WithCancel(context.Background())
			consumed := make(chan error, 1)
			go func() {
				consumed <- subject.Consume(ctx)
			}()
			DeferCleanup(func() {
				cancel()
				Eventually(consumed, "5s").Should(Receive(BeNil()))
			})

			Eventually(log.startedIDs).Should(Equal([]string{"A", "B"}))
			Eventually(done).Should(BeClosed())
		})
	})

	Context("on shutdown", func() {
		It("drops the pending writes and rejects later enqueues", func() {
			subject := queue.NewQueue()
			log := &applyLog{}
			release := make(chan struct{})
			DeferCleanup(func() {
				closeOnce(release)
			})

			ctx, cancel := context.WithCancel(context.Background())
			consumed := make(chan error, 1)
			go func() {
				consumed <- subject.Consume(ctx)
			}()

			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "A", Apply: stubbornApply(log, "A", release),
			})).To(Succeed())
			Eventually(log.startedIDs).Should(ContainElement("A"))

			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "B", Apply: recordingApply(log, "B"),
			})).To(Succeed())
			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "C", Apply: recordingApply(log, "C"),
			})).To(Succeed())

			cancel()
			Consistently(consumed, "200ms").ShouldNot(Receive())

			closeOnce(release)
			Eventually(consumed, "5s").Should(Receive(BeNil()))

			Expect(log.startedIDs()).To(Equal([]string{"A"}))

			err := subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "D", Apply: recordingApply(log, "D"),
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, queue.ErrClosed)).To(BeTrue())
			Expect(log.startedIDs()).To(Equal([]string{"A"}))
		})
	})

	Context("Consume", func() {
		It("rejects a second call", func() {
			subject := queue.NewQueue()
			log := &applyLog{}

			ctx, cancel := context.WithCancel(context.Background())
			consumed := make(chan error, 1)
			go func() {
				consumed <- subject.Consume(ctx)
			}()
			DeferCleanup(func() {
				cancel()
				Eventually(consumed, "5s").Should(Receive(BeNil()))
			})

			Expect(subject.Enqueue(context.Background(), queue.Write{
				Vault: "alpha", ItemID: "A", Apply: recordingApply(log, "A"),
			})).To(Succeed())
			// An applied write proves the first Consume stored its context.
			Eventually(log.finishedIDs).Should(ContainElement("A"))

			secondCtx, secondCancel := context.WithCancel(context.Background())
			DeferCleanup(secondCancel)

			err := subject.Consume(secondCtx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already consuming"))
		})
	})
})
