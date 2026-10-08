// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/websocket"
	websocketmocks "github.com/bborbe/vault-ui/pkg/websocket/mocks"
)

// sessionRegistryFrame is the wire shape of a refresh frame.
type sessionRegistryFrame struct {
	Type     string `json:"type"`
	TaskID   string `json:"task_id"`
	Vault    string `json:"vault"`
	ItemKind string `json:"item_kind"`
}

// fakeStore is a hand-written heartbeat.Store double whose live set a spec moves
// between polls.
type fakeStore struct {
	mu    sync.Mutex
	ids   []string
	known bool
}

func newFakeStore(ids ...string) *fakeStore {
	return &fakeStore{ids: ids, known: true}
}

func (s *fakeStore) LiveSessionIDs(_ context.Context) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.ids...), s.known
}

func (s *fakeStore) IsLive(_ context.Context, sessionID string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.ids {
		if id == sessionID {
			return true, s.known
		}
	}
	return false, s.known
}

func (s *fakeStore) set(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = ids
	s.known = true
}

var _ = Describe("CreateSessionStateWatcher", func() {
	// The poll interval is short so the spec drives the watcher without waiting
	// the production minute.
	const pollInterval = 20 * time.Millisecond

	It("seeds the state on the initial read and pushes two frames per real change", func() {
		loader := &mocks.Loader{}
		loader.GetAllVaultsReturns([]*config.Vault{{
			Name: "personal", Path: tempDir(),
		}}, nil)

		manager := &websocketmocks.WebsocketConnectionManager{}
		state := factory.CreateSessionState()
		store := newFakeStore("seed")

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			defer GinkgoRecover()
			_ = factory.CreateSessionStateWatcher(
				loader, manager, state, store, pollInterval,
			)(ctx)
		}()

		// The initial read populates the state and pushes nothing. Waiting for
		// it here means the change below cannot be absorbed by it.
		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("seed"))
		Expect(manager.BroadcastCallCount()).To(Equal(0))

		store.set("seed", "second")
		Eventually(manager.BroadcastCallCount, 5*time.Second, 20*time.Millisecond).Should(Equal(2))

		frames := [][]byte{
			manager.BroadcastArgsForCall(0),
			manager.BroadcastArgsForCall(1),
		}
		kinds := make([]string, 0, len(frames))
		for _, frame := range frames {
			var wire sessionRegistryFrame
			Expect(json.Unmarshal(frame, &wire)).To(Succeed())
			Expect(wire.Type).To(Equal("modified"))
			Expect(wire.TaskID).To(Equal(""))
			Expect(wire.Vault).To(Equal("personal"))
			kinds = append(kinds, wire.ItemKind)
		}
		Expect(kinds).To(ConsistOf("task", "goal"))

		// The frame shape is the existing watcher frame, key for key.
		Expect(frames[0]).To(Equal(websocket.WatcherFrame(ops.WatchEvent{
			Event: "modified", Vault: "personal", Type: "task",
		})))
		Expect(frames[1]).To(Equal(websocket.WatcherFrame(ops.WatchEvent{
			Event: "modified", Vault: "personal", Type: "goal",
		})))

		// A poll that reads the same set pushes no frame.
		Consistently(manager.BroadcastCallCount, 150*time.Millisecond).Should(Equal(2))

		// A third live session is two more frames.
		store.set("seed", "second", "third")
		Eventually(manager.BroadcastCallCount, 5*time.Second, 20*time.Millisecond).Should(Equal(4))
	})

	It("fails when the vault config cannot be loaded", func() {
		loader := &mocks.Loader{}
		loader.GetAllVaultsReturns(nil, os.ErrPermission)

		err := factory.CreateSessionStateWatcher(
			loader, &websocketmocks.WebsocketConnectionManager{},
			factory.CreateSessionState(), newFakeStore("seed"), pollInterval,
		)(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
