// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/mocks"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/factory"
	"github.com/bborbe/vault-ui/pkg/heartbeat"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pageindex"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/websocket"
)

// liveSessionID is the session id the pane specs resolve.
const liveSessionID = "11111111-1111-1111-1111-111111111111"

// recordingPaneResolver records single-session Resolve calls and batch
// ResolveAll calls separately, so a spec can tell a list read's one batch from
// a jump's one single-session resolve.
type recordingPaneResolver struct {
	panes        map[string]string
	resolveCalls []string
	allCalls     [][]string
}

func (r *recordingPaneResolver) Resolve(_ context.Context, sessionID string) (string, bool) {
	r.resolveCalls = append(r.resolveCalls, sessionID)
	pane, ok := r.panes[sessionID]
	return pane, ok
}

func (r *recordingPaneResolver) ResolveAll(
	_ context.Context,
	sessionIDs []string,
) map[string]string {
	r.allCalls = append(r.allCalls, append([]string(nil), sessionIDs...))
	resolved := map[string]string{}
	for _, sessionID := range sessionIDs {
		if pane, ok := r.panes[sessionID]; ok {
			resolved[sessionID] = pane
		}
	}
	return resolved
}

// paneFixture builds a vault holding one live task, so the board classifies it
// "live" once the attention store's live set is seeded into the session state.
func paneFixture() (config.Loader, string, string, string) {
	vaultDir := tempDir()
	Expect(os.MkdirAll(filepath.Join(vaultDir, "24 Tasks"), 0750)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(vaultDir, "23 Goals"), 0750)).To(Succeed())
	writeFile(
		vaultDir, "24 Tasks/LiveTask.md",
		"---\nstatus: in_progress\nclaude_session_id: "+liveSessionID+"\n---\n# LiveTask\n",
	)
	writeFile(vaultDir, "23 Goals/Goal A.md", "---\nstatus: in_progress\n---\n# Goal A\n")

	configPath := filepath.Join(tempDir(), "config.yaml")
	Expect(os.WriteFile(configPath, []byte("host: 127.0.0.1\n"), 0600)).To(Succeed())

	homeDir := tempDir()

	loader := &mocks.Loader{}
	loader.GetAllVaultsReturns([]*config.Vault{{
		Name:     "personal",
		Path:     vaultDir,
		TasksDir: "24 Tasks",
		GoalsDir: "23 Goals",
	}}, nil)
	loader.GetCurrentUserReturns("tester", nil)
	return loader, configPath, homeDir, vaultDir
}

// paneHandler wires CreateAPIHandler exactly as production does over a warmed
// page index, with the given pane resolver in the fourth position.
func paneHandler(
	loader config.Loader,
	configPath string,
	homeDir string,
	paneResolver *recordingPaneResolver,
) http.Handler {
	readiness := vaultui.NewReadiness()
	readiness.SetReady()
	pageIndex := factory.CreatePageIndex(
		pageindex.NewPageReader(storage.NewPageStorage(nil)), pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
	)
	Expect(
		factory.CreatePageIndexWarmup(loader, configPath, pageIndex)(context.Background()),
	).To(Succeed())

	// The board reads the live set from the session state, which production
	// keeps current from the attention store with the session-state watcher.
	// Seed it directly so the fixture's session id stays load-bearing.
	sessionState := factory.CreateSessionState()
	sessionState.Replace([]heartbeat.Session{{ID: liveSessionID}})
	sessionSnapshot := factory.CreateSessionSnapshot(sessionState)
	Expect(sessionSnapshot.RefreshOnce(context.Background())).To(Succeed())

	return factory.CreateAPIHandler(
		loader, configPath, statuscache.NewCache(), paneResolver,
		launchregistry.NewRegistry(), sessionlock.NewRegistry(), homeDir, readiness,
		websocket.NewConnectionManager(websocket.NewMetrics()), pageIndex,
		sessionSnapshot, startWriteQueue(),
	)
}

// listKeys returns the key set of every element in a JSON array body.
func listKeys(body string) []string {
	var elements []map[string]any
	Expect(json.Unmarshal([]byte(body), &elements)).To(Succeed())
	Expect(elements).NotTo(BeEmpty())
	keys := make([]string, 0)
	for _, element := range elements {
		for key := range element {
			keys = append(keys, key)
		}
	}
	return keys
}

// listElements decodes a JSON array body into its element maps.
func listElements(body string) []map[string]any {
	var elements []map[string]any
	Expect(json.Unmarshal([]byte(body), &elements)).To(Succeed())
	return elements
}

var _ = Describe("Lazy pane resolution", func() {
	It("resolves panes in one batch on a list read and one session on a jump", func() {
		loader, configPath, homeDir, _ := paneFixture()
		recorder := &recordingPaneResolver{
			panes: map[string]string{liveSessionID: "7"},
		}
		handler := paneHandler(loader, configPath, homeDir, recorder)

		listRecorder := httptest.NewRecorder()
		handler.ServeHTTP(
			listRecorder,
			httptest.NewRequest(http.MethodGet, "/api/tasks?vault=personal", nil),
		)
		Expect(listRecorder.Code).To(Equal(http.StatusOK))
		// Fixture guard: the task must classify live, otherwise the board would
		// never resolve a pane at all.
		Expect(listRecorder.Body.String()).To(ContainSubstring(`"session_state":"live"`))
		// The list read makes at most one batch call and never a single-session
		// resolve: one shell-out per build, not one per session.
		Expect(recorder.allCalls).To(HaveLen(1))
		Expect(recorder.allCalls[0]).To(Equal([]string{liveSessionID}))
		Expect(recorder.resolveCalls).To(BeEmpty())

		// Positive control: the same resolver fires once on a real jump, so the
		// assertions above cannot pass with a dead double. The status is
		// deliberately not asserted: the pane resolves before the jump credential
		// is read, and no credential exists under the temp home.
		jumpRecorder := httptest.NewRecorder()
		handler.ServeHTTP(
			jumpRecorder,
			httptest.NewRequest(
				http.MethodPost, "/api/tasks/LiveTask/jump?vault=personal", nil,
			),
		)
		Expect(recorder.resolveCalls).To(HaveLen(1))
		Expect(recorder.resolveCalls[0]).To(Equal(liveSessionID))
	})

	It("carries jump_pane on a live task only when a pane resolves", func() {
		loader, configPath, homeDir, _ := paneFixture()

		withPane := &recordingPaneResolver{
			panes: map[string]string{liveSessionID: "7"},
		}
		handler := paneHandler(loader, configPath, homeDir, withPane)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/api/tasks?vault=personal", nil),
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		elements := listElements(recorder.Body.String())
		Expect(elements).To(HaveLen(1))
		Expect(elements[0]).To(HaveKeyWithValue("jump_pane", "7"))

		withoutPane := &recordingPaneResolver{panes: map[string]string{}}
		handler = paneHandler(loader, configPath, homeDir, withoutPane)
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/api/tasks?vault=personal", nil),
		)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		elements = listElements(recorder.Body.String())
		Expect(elements).To(HaveLen(1))
		// The key is always emitted; a live card with no pane carries null.
		Expect(elements[0]).To(HaveKey("jump_pane"))
		Expect(elements[0]["jump_pane"]).To(BeNil())
	})

	It("carries no pane-bearing key in a goal list response", func() {
		loader, configPath, _ := apiFixture()
		handler := newTestAPIHandler(loader, configPath)

		target := "/api/goals?vault=personal"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		Expect(recorder.Code).To(Equal(http.StatusOK), "%s", target)

		keys := listKeys(recorder.Body.String())
		Expect(keys).NotTo(BeEmpty(), "%s", target)
		for _, key := range keys {
			Expect(strings.Contains(strings.ToLower(key), "pane")).To(
				BeFalse(), "%s carries %q", target, key,
			)
		}
	})
})
