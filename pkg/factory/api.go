// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/activity"
	"github.com/bborbe/vault-ui/pkg/board"
	"github.com/bborbe/vault-ui/pkg/handler"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/pane"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
	"github.com/bborbe/vault-ui/pkg/websocket"
	staticui "github.com/bborbe/vault-ui/src/vault_ui"
)

// defaultAPIListen is the production API listen address.
const defaultAPIListen = ":8000"

// vaultProvider resolves the board's vaults from vault-cli's config, merged
// with vault-ui's own config.yaml. It never caches: a vault added to vault-cli
// is visible on the next request.
type vaultProvider struct {
	loader     config.Loader
	configPath string
}

func (p *vaultProvider) Vaults(ctx context.Context) ([]board.Vault, error) {
	cfg, err := vaultconfig.Load(ctx, p.loader, p.configPath)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "load vault config")
	}
	vaults := make([]board.Vault, 0, len(cfg.Vaults))
	for _, vault := range cfg.Vaults {
		vaults = append(vaults, board.Vault{
			Name:              vault.Name,
			VaultName:         vault.VaultName,
			Path:              vault.Path,
			TasksFolder:       vault.TasksFolder,
			GoalsFolder:       vault.GoalsFolder,
			TopicsFolder:      vault.TopicsFolder,
			ClaudeScript:      vault.ClaudeScript,
			SessionProjectDir: vault.SessionProjectDir,
		})
	}
	return vaults, nil
}

// opsProvider builds the vault-cli read operations for a vault.
type opsProvider struct{}

func (opsProvider) List(vault board.Vault) ops.ListOperation {
	return ops.NewListOperation(storage.NewPageStorage(vault.StorageConfig()))
}

func (opsProvider) TopicShow(vault board.Vault) ops.EntityShowOperation {
	return ops.NewTopicShowOperation(storage.NewTopicStorage(vault.StorageConfig()))
}

// sessionSignals reads the Claude session registry and the live process table.
type sessionSignals struct {
	homeDir string
}

func (s sessionSignals) RegistrySessionIDs(ctx context.Context) []string {
	return activity.ReadRegistrySessionIDs(ctx, filepath.Join(s.homeDir, ".claude", "sessions"))
}

func (s sessionSignals) ResumeSessionIDs(ctx context.Context) []string {
	output, err := session.NewPSScanner("-axww", "-o", "args=")(ctx)
	if err != nil {
		return nil
	}
	return session.ParseLiveSessionIDs(output)
}

// paneResolver resolves a live session to its WezTerm pane via the
// supervisor's who-needs-me.py.
type paneResolver struct {
	homeDir     string
	pluginRoot  string
	interpreter string
}

func (p paneResolver) Resolve(ctx context.Context, sessionID string) (string, bool) {
	script := pane.WhoNeedsMePath(p.pluginRoot, p.homeDir)
	env := pane.BuildSubprocessEnv(
		os.Environ(), p.homeDir, pane.DefaultWeztermBundleDir, pane.PidAlive,
	)
	return pane.ResolvePaneID(
		ctx, p.interpreter, script, sessionID, env, pane.DefaultResolveTimeout,
	)
}

// CreateVaultUIConfigPath resolves vault-ui's config.yaml path, XDG-first.
func CreateVaultUIConfigPath(homeDir string) string {
	xdg := filepath.Join(homeDir, ".config", "vault-ui", "config.yaml")
	legacy := filepath.Join(homeDir, "config.yaml")
	return vaultconfig.ResolveDefaultConfigPath(xdg, legacy)
}

// CreateAPIListen returns the API listen address from VAULT_UI_LISTEN,
// defaulting to :8000.
func CreateAPIListen() string {
	if listen := os.Getenv("VAULT_UI_LISTEN"); listen != "" {
		return listen
	}
	return defaultAPIListen
}

// CreateStaticFS returns the embedded frozen frontend tree, rooted at the
// static directory.
func CreateStaticFS() fs.FS {
	sub, err := fs.Sub(staticui.FS, "static")
	if err != nil {
		// Unreachable: the embed tree is fixed at build time.
		panic(err)
	}
	return sub
}

// CreateAPIHandler builds the :8000 API router.
func CreateAPIHandler(
	loader config.Loader,
	configPath string,
	cache statuscache.Cache,
	launches launchregistry.Registry,
	homeDir string,
	readiness vaultui.Readiness,
	manager websocket.ConnectionManager,
) http.Handler {
	service := board.New(board.Deps{
		Vaults:  &vaultProvider{loader: loader, configPath: configPath},
		Ops:     opsProvider{},
		Cache:   cache,
		Launch:  launches,
		Clock:   libtime.NewCurrentDateTime(),
		Signals: sessionSignals{homeDir: homeDir},
		Pane:    paneResolver{homeDir: homeDir, interpreter: "python3"},
		HomeDir: homeDir,
	})
	mutationsService := CreateMutationService(
		loader, configPath, cache, launches, sessionlock.NewRegistry(), homeDir,
		connectionEventPublisher{manager: manager},
	)
	return handler.CreateHTTPRouter(service, mutationsService, CreateStaticFS(), readiness, manager)
}

// CreateConnectionManager returns the bounded, non-blocking WebSocket
// connection manager shared by the /ws handler, the watcher, and the mutation
// publisher.
func CreateConnectionManager() websocket.ConnectionManager {
	return websocket.NewConnectionManager(websocket.NewMetrics())
}

// CreateWatcher returns a run.Func that watches the configured vaults with
// vault-cli's in-process watcher and broadcasts one frame per change. No
// vault-cli subprocess is spawned.
func CreateWatcher(loader config.Loader, manager websocket.ConnectionManager) run.Func {
	return func(ctx context.Context) error {
		vaults, err := loader.GetAllVaults(ctx)
		if err != nil {
			return errors.Wrap(ctx, err, "load vaults for watcher")
		}
		targets := buildWatchTargets(vaults)
		glog.V(2).Infof("starting vault watcher for %d vaults", len(targets))
		return ops.NewWatchOperation().Execute(ctx, targets,
			func(event ops.WatchEvent) error {
				glog.V(3).Infof("watcher event %s %s/%s", event.Event, event.Vault, event.Name)
				manager.Broadcast(websocket.WatcherFrame(event))
				return nil
			},
		)
	}
}

// buildWatchTargets assembles the vault-cli watch targets for the entity kinds
// the Python backend watches (task, goal, theme, objective), mirroring
// buildWatchTargets in vault-cli's pkg/cli/cli.go.
func buildWatchTargets(vaults []*config.Vault) []ops.WatchTarget {
	targets := make([]ops.WatchTarget, 0, len(vaults))
	for _, vault := range vaults {
		targets = append(targets, ops.WatchTarget{
			VaultPath: vault.Path,
			VaultName: vault.Name,
			WatchDirs: []ops.WatchDir{
				{Dir: vault.GetTasksDir(), Kind: "task"},
				{Dir: vault.GetGoalsDir(), Kind: "goal"},
				{Dir: vault.GetThemesDir(), Kind: "theme"},
				{Dir: vault.GetObjectivesDir(), Kind: "objective"},
			},
		})
	}
	return targets
}

// CreateStatusCacheLoader returns a run.Func that loads every vault's status
// and claude_session_started markers into the cache once at startup.
func CreateStatusCacheLoader(
	loader config.Loader,
	configPath string,
	cache statuscache.Cache,
) run.Func {
	return func(ctx context.Context) error {
		cfg, err := vaultconfig.Load(ctx, loader, configPath)
		if err != nil {
			return errors.Wrap(ctx, err, "load vault config")
		}
		for _, vault := range cfg.Vaults {
			if loadErr := cache.LoadVault(vault.Name, vault.Path, vault.TasksFolder); loadErr != nil {
				glog.Warningf("load status cache for vault %s: %v", vault.Name, loadErr)
			}
		}
		return nil
	}
}

// CreateAPIServer returns a run.Func serving the API router on listen.
func CreateAPIServer(listen string, apiHandler http.Handler) run.Func {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		glog.V(2).Infof("starting api server listen on %s", listen)
		return libhttp.NewServer(listen, apiHandler).Run(ctx)
	}
}
