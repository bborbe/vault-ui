// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	"context"

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/config"
	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/google/uuid"

	vaultui "github.com/bborbe/vault-ui/pkg"
	"github.com/bborbe/vault-ui/pkg/launchregistry"
	"github.com/bborbe/vault-ui/pkg/mutations"
	"github.com/bborbe/vault-ui/pkg/pane"
	"github.com/bborbe/vault-ui/pkg/session"
	"github.com/bborbe/vault-ui/pkg/sessionlock"
	"github.com/bborbe/vault-ui/pkg/sigterm"
	"github.com/bborbe/vault-ui/pkg/statuscache"
	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// mutationConfigProvider resolves the merged vault config for the mutation
// service. It never caches.
type mutationConfigProvider struct {
	loader     config.Loader
	configPath string
}

func (p *mutationConfigProvider) Load(ctx context.Context) (*vaultconfig.Config, error) {
	return vaultconfig.Load(ctx, p.loader, p.configPath)
}

// mutationOpsFactory builds a vault-cli op set for one resolved vault, wiring
// the session starter and resumer from the vault's claude script.
func mutationOpsFactory(vault vaultconfig.Vault) vaultui.OpSet {
	cliVault := &config.Vault{
		Path:              vault.Path,
		Name:              vault.Name,
		TasksDir:          vault.TasksFolder,
		GoalsDir:          vault.GoalsFolder,
		TopicsDir:         vault.TopicsFolder,
		ClaudeScript:      vault.ClaudeScript,
		SessionProjectDir: vault.SessionProjectDir,
	}
	locker := ops.NewSessionLocker()
	starter := ops.NewClaudeSessionStarter(vault.ClaudeScript, locker)
	resumer := ops.NewClaudeResumer(vault.ClaudeScript, locker)
	// The launcher factory builds the starter/resumer pair for a task that
	// resolved to a different launcher than the vault's claude_script. It shares
	// the default pair's locker so both paths serialize on the same session
	// files.
	launcherFactory := func(script string) (ops.ClaudeSessionStarter, ops.ClaudeResumer) {
		return ops.NewClaudeSessionStarter(script, locker), ops.NewClaudeResumer(script, locker)
	}
	return CreateOpSet(
		cliVault,
		libtime.NewCurrentDateTime(),
		ops.NewEscalationPublisher("", "", nil),
		starter,
		resumer,
		ops.NewInteractionCounter("", ""),
		uuid.NewString,
		launcherFactory,
	)
}

// paneJumpClient reads the shared jump credential and performs the jump through
// the pane package.
type paneJumpClient struct {
	homeDir string
}

func (c paneJumpClient) ReadToken() (string, bool) {
	return pane.ReadJumpToken(pane.JumpTokenPath(c.homeDir))
}

func (c paneJumpClient) Perform(ctx context.Context, paneID, token string) error {
	return pane.PerformJump(
		ctx, pane.DefaultJumpServerURL, paneID, token, pane.DefaultJumpTimeout,
	)
}

// CreateMutationService builds the mutating board service.
func CreateMutationService(
	loader config.Loader,
	configPath string,
	cache statuscache.Cache,
	launches launchregistry.Registry,
	locks sessionlock.Registry,
	homeDir string,
	paneResolver mutations.PaneResolver,
	publisher mutations.EventPublisher,
	index mutations.IndexInvalidator,
	writeQueue mutations.WriteQueue,
) mutations.Service {
	return mutations.New(mutations.Deps{
		Config:    &mutationConfigProvider{loader: loader, configPath: configPath},
		Ops:       mutationOpsFactory,
		Cache:     cache,
		Launch:    launches,
		Locks:     locks,
		Publisher: publisher,
		Index:     index,
		Queue:     writeQueue,
		Clock:     libtime.NewCurrentDateTime(),
		Scanner:   session.NewPSScanner("-axww", "-o", "args="),
		Signaler:  sigterm.NewProcessSignaler(),
		Pane:      paneResolver,
		Jump:      paneJumpClient{homeDir: homeDir},
		HomeDir:   homeDir,
		WatcherNames: func(ctx context.Context) []string {
			cfg, err := vaultconfig.Load(ctx, loader, configPath)
			if err != nil {
				return nil
			}
			names := make([]string, 0, len(cfg.Vaults))
			for _, vault := range cfg.Vaults {
				names = append(names, vault.Name)
			}
			return names
		},
	})
}
