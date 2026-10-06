// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pane_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/pane"
)

// fakeExec is a hand-written double for pane.ExecFunc: a struct of function
// fields so a nil field panics on an unexpected call. This repo has no
// `make generate` target and counterfeiter is not a module dependency, so a
// counterfeiter fake cannot be generated here.
type fakeExec struct {
	exec func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

func (f fakeExec) Exec(
	ctx context.Context,
	env []string,
	name string,
	args ...string,
) ([]byte, error) {
	if f.exec == nil {
		panic("fakeExec.Exec called with no exec func")
	}
	return f.exec(ctx, env, name, args...)
}

// fakeRegistryNames is a hand-written double for pane.RegistryNamesFunc: a
// struct of function fields so a nil field panics on an unexpected call.
type fakeRegistryNames struct {
	names func(ctx context.Context, dir string) map[string]string
}

func (f fakeRegistryNames) RegistryNames(ctx context.Context, dir string) map[string]string {
	if f.names == nil {
		panic("fakeRegistryNames.RegistryNames called with no names func")
	}
	return f.names(ctx, dir)
}

// execCall is one recorded invocation of the process boundary.
type execCall struct {
	env  []string
	name string
	args []string
}

// execRecorder captures every argv/env handed to Exec.
type execRecorder struct {
	mu    sync.Mutex
	calls []execCall
}

func (r *execRecorder) record(env []string, name string, args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, execCall{env: env, name: name, args: args})
}

func (r *execRecorder) snapshot() []execCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]execCall(nil), r.calls...)
}

// registryRecorder captures every directory handed to the registry reader.
type registryRecorder struct {
	mu   sync.Mutex
	dirs []string
}

func (r *registryRecorder) record(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirs = append(r.dirs, dir)
}

func (r *registryRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dirs...)
}

// writeRegistryEntry writes one `<name>` registry file under dir.
func writeRegistryEntry(dir, name, body string) string {
	path := filepath.Join(dir, name)
	ExpectWithOffset(1, os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
	return path
}

const fullSessionID = "e0930886-0843-4ca9-adfa-58819443c032"

var _ = Describe("Resolver", func() {
	// newResolver wires both doubles and returns the resolver plus its
	// recorders, so every entry asserts the whole call shape.
	newResolver := func(
		params pane.ResolverParams,
		stdout string,
		execErr error,
		names map[string]string,
	) (pane.Resolver, *execRecorder, *registryRecorder) {
		execRec := &execRecorder{}
		registryRec := &registryRecorder{}
		params.Exec = fakeExec{
			exec: func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
				execRec.record(env, name, args)
				return []byte(stdout), execErr
			},
		}.Exec
		params.RegistryNames = fakeRegistryNames{
			names: func(_ context.Context, dir string) map[string]string {
				registryRec.record(dir)
				return names
			},
		}.RegistryNames
		return pane.NewResolver(params), execRec, registryRec
	}

	baseParams := func() pane.ResolverParams {
		return pane.ResolverParams{
			HomeDir:     "/home/operator",
			BundleDir:   "/no/such/bundle",
			RegistryDir: "/home/operator/.claude/sessions",
			Timeout:     time.Second,
		}
	}

	type resolveCase struct {
		registry          map[string]string
		sessionID         string
		stdout            string
		execErr           error
		wantPane          string
		wantOK            bool
		wantExecCalls     int
		wantRegistryCalls int
	}

	DescribeTable("Resolve",
		func(tc resolveCase) {
			resolver, execRec, registryRec := newResolver(
				baseParams(), tc.stdout, tc.execErr, tc.registry,
			)

			paneID, ok := resolver.Resolve(context.Background(), tc.sessionID)

			Expect(ok).To(Equal(tc.wantOK))
			Expect(paneID).To(Equal(tc.wantPane))
			Expect(execRec.snapshot()).To(HaveLen(tc.wantExecCalls))
			Expect(registryRec.snapshot()).To(HaveLen(tc.wantRegistryCalls))
		},
		Entry("an exact full-id match", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         fullSessionID,
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantPane:          "7",
			wantOK:            true,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("a unique 8-char prefix match", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         "e0930886",
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantPane:          "7",
			wantOK:            true,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("a case-insensitive prefix match", resolveCase{
			registry:          map[string]string{"E0930886-0843-4ca9-adfa-58819443c032": "Fleet"},
			sessionID:         "e0930886",
			stdout:            `[{"pane_id":9,"title":"Fleet"}]`,
			wantPane:          "9",
			wantOK:            true,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("two registry entries sharing the prefix", resolveCase{
			registry: map[string]string{
				"e0930886-0843-4ca9-adfa-58819443c032": "Fleet Manager",
				"e0930886-9999-4ca9-adfa-58819443c099": "Other",
			},
			sessionID:         "e0930886",
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     0,
			wantRegistryCalls: 1,
		}),
		Entry("an unknown id", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         "deadbeef",
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     0,
			wantRegistryCalls: 1,
		}),
		Entry("an empty id", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         "",
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     0,
			wantRegistryCalls: 0,
		}),
		Entry("a registry name and a title carrying different status glyphs", resolveCase{
			registry:          map[string]string{fullSessionID: "✳ Fleet Manager"},
			sessionID:         fullSessionID,
			stdout:            `[{"pane_id":7,"title":"◉ Fleet Manager"}]`,
			wantPane:          "7",
			wantOK:            true,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("a title match that yields two panes", resolveCase{
			registry:  map[string]string{fullSessionID: "Fleet Manager"},
			sessionID: fullSessionID,
			stdout: `[{"pane_id":7,"title":"✳ Fleet Manager"},` +
				`{"pane_id":8,"title":"◉ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("a title match that yields none", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         fullSessionID,
			stdout:            `[{"pane_id":7,"title":"✳ Someone Else"}]`,
			wantOK:            false,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("an empty registry name", resolveCase{
			registry:          map[string]string{fullSessionID: ""},
			sessionID:         fullSessionID,
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     0,
			wantRegistryCalls: 1,
		}),
		Entry("a name that is only a status glyph", resolveCase{
			registry:          map[string]string{fullSessionID: "✳"},
			sessionID:         fullSessionID,
			stdout:            `[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			wantOK:            false,
			wantExecCalls:     0,
			wantRegistryCalls: 1,
		}),
		Entry("a non-zero wezterm exit", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         fullSessionID,
			execErr:           errors.New("exit status 1"),
			wantOK:            false,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("malformed JSON", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         fullSessionID,
			stdout:            `{"not":"an array"`,
			wantOK:            false,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
		Entry("a context deadline error", resolveCase{
			registry:          map[string]string{fullSessionID: "Fleet Manager"},
			sessionID:         fullSessionID,
			execErr:           context.DeadlineExceeded,
			wantOK:            false,
			wantExecCalls:     1,
			wantRegistryCalls: 1,
		}),
	)

	It("hands wezterm cli list --format json as the exact argv", func() {
		resolver, execRec, _ := newResolver(
			baseParams(),
			`[{"pane_id":7,"title":"✳ Fleet Manager"}]`,
			nil,
			map[string]string{fullSessionID: "Fleet Manager"},
		)

		_, ok := resolver.Resolve(context.Background(), fullSessionID)
		Expect(ok).To(BeTrue())

		calls := execRec.snapshot()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].name).To(Equal("wezterm"))
		Expect(calls[0].args).To(Equal([]string{"cli", "list", "--format", "json"}))
		Expect(calls[0].env).NotTo(BeEmpty())
	})

	It("reads the registry under <homeDir>/.claude/sessions when no dir is configured", func() {
		registryRec := &registryRecorder{}
		resolver := pane.NewResolver(pane.ResolverParams{
			HomeDir: "/home/operator",
			Exec: fakeExec{
				exec: func(context.Context, []string, string, ...string) ([]byte, error) {
					return []byte("[]"), nil
				},
			}.Exec,
			RegistryNames: fakeRegistryNames{
				names: func(_ context.Context, dir string) map[string]string {
					registryRec.record(dir)
					return nil
				},
			}.RegistryNames,
		})

		_, ok := resolver.Resolve(context.Background(), fullSessionID)
		Expect(ok).To(BeFalse())
		Expect(registryRec.snapshot()).To(Equal([]string{
			filepath.Join("/home/operator", ".claude", "sessions"),
		}))
	})

	It("accepts a zero timeout and a missing registry dir without resolving", func() {
		resolver := pane.NewResolver(pane.ResolverParams{
			HomeDir: "/home/operator",
			Exec: fakeExec{
				exec: func(context.Context, []string, string, ...string) ([]byte, error) {
					panic("Exec must not be called without a registry match")
				},
			}.Exec,
			RegistryNames: fakeRegistryNames{
				names: func(context.Context, string) map[string]string { return nil },
			}.RegistryNames,
		})

		_, ok := resolver.Resolve(context.Background(), fullSessionID)
		Expect(ok).To(BeFalse())
	})

	Describe("the real registry reader", func() {
		It("reads session names from <dir>/<pid>.json and skips the .key files", func() {
			dir := GinkgoT().TempDir()
			writeRegistryEntry(
				dir,
				"10736.json",
				`{"pid":10736,"sessionId":"`+fullSessionID+`","name":"Fleet Manager"}`,
			)
			writeRegistryEntry(dir, "10736.abcdef.key", `{"sessionId":"ignored","name":"Ignored"}`)

			resolver := pane.NewResolver(pane.ResolverParams{
				HomeDir:     "/home/operator",
				RegistryDir: dir,
				Exec: fakeExec{
					exec: func(context.Context, []string, string, ...string) ([]byte, error) {
						return []byte(`[{"pane_id":7,"title":"✳ Fleet Manager"}]`), nil
					},
				}.Exec,
			})

			paneID, ok := resolver.Resolve(context.Background(), "e0930886")
			Expect(ok).To(BeTrue())
			Expect(paneID).To(Equal("7"))
		})

		It("skips a directory, a non-JSON entry and an entry with no sessionId", func() {
			dir := GinkgoT().TempDir()
			writeRegistryEntry(dir, "bad.json", `{not json`)
			writeRegistryEntry(dir, "empty.json", `{"pid":1,"name":"No Session Id"}`)
			Expect(os.MkdirAll(filepath.Join(dir, "subdir.json"), 0o750)).To(Succeed())
			writeRegistryEntry(
				dir,
				"good.json",
				`{"sessionId":"`+fullSessionID+`","name":"Fleet Manager"}`,
			)

			resolver := pane.NewResolver(pane.ResolverParams{
				HomeDir:     "/home/operator",
				RegistryDir: dir,
				Exec: fakeExec{
					exec: func(context.Context, []string, string, ...string) ([]byte, error) {
						return []byte(`[{"pane_id":7,"title":"✳ Fleet Manager"}]`), nil
					},
				}.Exec,
			})

			paneID, ok := resolver.Resolve(context.Background(), fullSessionID)
			Expect(ok).To(BeTrue())
			Expect(paneID).To(Equal("7"))
		})

		It("returns an empty map for a missing registry directory", func() {
			resolver := pane.NewResolver(pane.ResolverParams{
				HomeDir:     "/home/operator",
				RegistryDir: filepath.Join(GinkgoT().TempDir(), "missing"),
				Exec: fakeExec{
					exec: func(context.Context, []string, string, ...string) ([]byte, error) {
						panic("Exec must not be called without a registry match")
					},
				}.Exec,
			})

			_, ok := resolver.Resolve(context.Background(), fullSessionID)
			Expect(ok).To(BeFalse())
		})

		It("returns an empty map when the context is already cancelled", func() {
			dir := GinkgoT().TempDir()
			writeRegistryEntry(
				dir,
				"10736.json",
				`{"sessionId":"`+fullSessionID+`","name":"Fleet Manager"}`,
			)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			resolver := pane.NewResolver(pane.ResolverParams{
				HomeDir:     "/home/operator",
				RegistryDir: dir,
				Exec: fakeExec{
					exec: func(context.Context, []string, string, ...string) ([]byte, error) {
						panic("Exec must not be called with a cancelled context")
					},
				}.Exec,
			})

			_, ok := resolver.Resolve(ctx, fullSessionID)
			Expect(ok).To(BeFalse())
		})
	})

	Describe("the default Exec", func() {
		It("reports a failure rather than spawning when the context is already cancelled", func() {
			resolver := pane.NewResolver(pane.ResolverParams{
				HomeDir:     "/home/operator",
				RegistryDir: "/nowhere",
				RegistryNames: fakeRegistryNames{
					names: func(context.Context, string) map[string]string {
						return map[string]string{fullSessionID: "Fleet Manager"}
					},
				}.RegistryNames,
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, ok := resolver.Resolve(ctx, fullSessionID)
			Expect(ok).To(BeFalse())
		})
	})
})
