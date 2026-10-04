// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionlock_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/sessionlock"
)

// mustLock acquires the lock and fails the spec when the acquire errors.
func mustLock(ctx context.Context, registry sessionlock.Registry, vault, itemID string) func() {
	release, err := registry.Lock(ctx, vault, itemID)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return release
}

var _ = Describe("SessionLockRegistry", func() {
	DescribeTable("SessionLockRegistry",
		func(body func(registry sessionlock.Registry, baseline int)) {
			registry := sessionlock.NewRegistry()
			body(registry, registry.Size())
		},
		Entry("evict-only-at-holder-count-zero", func(registry sessionlock.Registry, baseline int) {
			ctx := context.Background()
			releaseFirst := mustLock(ctx, registry, "vault", "task")
			Expect(registry.Size()).To(Equal(baseline + 1))

			waiterStarted := make(chan struct{})
			waiterAcquired := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				close(waiterStarted)
				releaseSecond, err := registry.Lock(ctx, "vault", "task")
				if err != nil {
					return
				}
				close(waiterAcquired)
				releaseSecond()
			}()

			<-waiterStarted
			Consistently(waiterAcquired, "50ms").ShouldNot(BeClosed())
			Expect(registry.Size()).To(Equal(baseline + 1))

			releaseFirst()
			Eventually(waiterAcquired).Should(BeClosed())
			Eventually(registry.Size).Should(Equal(baseline))
		}),
		Entry("cancelled-waiter-does-not-wedge", func(registry sessionlock.Registry, baseline int) {
			ctx := context.Background()
			releaseFirst := mustLock(ctx, registry, "vault", "task")

			waiterCtx, cancelWaiter := context.WithCancel(ctx)
			type lockResult struct {
				release func()
				err     error
			}
			result := make(chan lockResult, 1)
			waiterStarted := make(chan struct{})
			go func() {
				close(waiterStarted)
				release, err := registry.Lock(waiterCtx, "vault", "task")
				result <- lockResult{release: release, err: err}
			}()

			<-waiterStarted
			Consistently(result, "50ms").ShouldNot(Receive())
			cancelWaiter()

			var cancelled lockResult
			Eventually(result).Should(Receive(&cancelled))
			Expect(cancelled.err).To(MatchError(context.Canceled))
			Expect(cancelled.release).To(BeNil())
			Expect(registry.Size()).To(Equal(baseline + 1))

			releaseFirst()
			Eventually(registry.Size).Should(Equal(baseline))

			// A later acquirer still obtains the lock and the entry evicts cleanly.
			releaseLater := mustLock(ctx, registry, "vault", "task")
			Expect(registry.Size()).To(Equal(baseline + 1))
			releaseLater()
			Eventually(registry.Size).Should(Equal(baseline))
		}),
	)

	It("serialises a second acquire for the same key", func() {
		registry := sessionlock.NewRegistry()
		ctx := context.Background()

		var (
			mu     sync.Mutex
			events []string
		)
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		}
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string{}, events...)
		}

		releaseFirst := mustLock(ctx, registry, "vault", "task")
		record("first-in")

		secondStarted := make(chan struct{})
		secondDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			close(secondStarted)
			releaseSecond, err := registry.Lock(ctx, "vault", "task")
			if err != nil {
				close(secondDone)
				return
			}
			record("second-in")
			record("second-out")
			releaseSecond()
			close(secondDone)
		}()

		<-secondStarted
		Consistently(snapshot, "50ms").Should(Equal([]string{"first-in"}))
		record("first-out")
		releaseFirst()
		Eventually(secondDone).Should(BeClosed())
		Expect(snapshot()).To(Equal([]string{"first-in", "first-out", "second-in", "second-out"}))
	})

	It("does not serialise locks for different keys", func() {
		registry := sessionlock.NewRegistry()
		ctx := context.Background()

		releaseA := mustLock(ctx, registry, "vault", "a")
		acquiredB := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			releaseB, err := registry.Lock(ctx, "vault", "b")
			if err != nil {
				return
			}
			close(acquiredB)
			releaseB()
		}()

		Eventually(acquiredB).Should(BeClosed())
		releaseA()
	})
})
