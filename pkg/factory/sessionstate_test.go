// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// writeSessionRegistryEntry atomically writes one `<name>.json` registry entry.
// The write goes to a dot-prefixed temp file first and is renamed into place: a
// plain truncating write would expose a momentarily empty file to the watcher's
// read, which registers as a spurious change. The temp file does not end in
// `.json`, so the registry reader ignores it.
func writeSessionRegistryEntry(dir, name, sessionID string) {
	content := []byte(`{"sessionId":"` + sessionID + `"}`)
	tmp := filepath.Join(dir, "."+name+".json.tmp")
	Expect(os.WriteFile(tmp, content, 0600)).To(Succeed())
	Expect(os.Rename(tmp, filepath.Join(dir, name+".json"))).To(Succeed())
}

var _ = Describe("CreateSessionStateWatcher", func() {
	It("seeds the state on the initial read and pushes two frames per real change", func() {
		homeDir := tempDir()
		registryDir := filepath.Join(homeDir, ".claude", "sessions")
		Expect(os.MkdirAll(registryDir, 0750)).To(Succeed())
		writeSessionRegistryEntry(registryDir, "seed", "seed")

		loader := &mocks.Loader{}
		loader.GetAllVaultsReturns([]*config.Vault{{
			Name: "personal", Path: tempDir(),
		}}, nil)

		manager := &websocketmocks.WebsocketConnectionManager{}
		state := factory.CreateSessionState()

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go func() {
			defer GinkgoRecover()
			_ = factory.CreateSessionStateWatcher(loader, manager, state, homeDir)(ctx)
		}()

		// The initial read populates the state and pushes nothing. Waiting for
		// it here means the write below cannot be absorbed by it.
		Eventually(func() []string { return state.RegistrySessionIDs(ctx) }).
			Should(ConsistOf("seed"))
		Expect(manager.BroadcastCallCount()).To(Equal(0))

		// The watch may not be armed yet, so rewrite on every poll: an
		// identical rewrite leaves the set unchanged and cannot inflate the
		// count once the event has been observed.
		Eventually(func() int {
			writeSessionRegistryEntry(registryDir, "2", "second")
			return manager.BroadcastCallCount()
		}, 5*time.Second, 20*time.Millisecond).Should(Equal(2))

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

		// A rewrite that changes nothing pushes no frame.
		writeSessionRegistryEntry(registryDir, "2", "second")
		Consistently(manager.BroadcastCallCount, 300*time.Millisecond).Should(Equal(2))

		// A third live session is two more frames.
		Eventually(func() int {
			writeSessionRegistryEntry(registryDir, "3", "third")
			return manager.BroadcastCallCount()
		}, 5*time.Second, 20*time.Millisecond).Should(Equal(4))
	})

	It("fails when the vault config cannot be loaded", func() {
		loader := &mocks.Loader{}
		loader.GetAllVaultsReturns(nil, os.ErrPermission)

		err := factory.CreateSessionStateWatcher(
			loader, &websocketmocks.WebsocketConnectionManager{},
			factory.CreateSessionState(), tempDir(),
		)(context.Background())
		Expect(err).To(HaveOccurred())
	})
})
