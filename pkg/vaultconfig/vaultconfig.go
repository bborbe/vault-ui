// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vaultconfig reproduces the Python vault_ui.config merge: it reads
// vault-ui's own config.yaml for host/port/max_concurrent_sessions, the
// vault-cli path, and the optional vaults: override block, then merges that with
// the injected vault-cli config.Loader's vaults and current user.
//
// The vault-cli side is the injected loader — this package never spawns
// vault-cli. A vault-cli vault whose tasks folder is absent on disk is skipped
// rather than failing the whole board.
package vaultconfig

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/config"
	"go.yaml.in/yaml/v3"
)

// defaultVaultCLIPath is the vault-cli binary name used when config.yaml omits
// vault_cli_path.
const defaultVaultCLIPath = "vault-cli"

// Vault is one resolved vault.
type Vault struct {
	Name              string
	Path              string
	TasksFolder       string
	GoalsFolder       string
	VaultName         string
	ClaudeScript      string
	VaultCLIPath      string
	SessionProjectDir string
	TopicsFolder      string
}

// Config is the merged application configuration.
type Config struct {
	Vaults                []Vault
	Host                  string
	Port                  int
	MaxConcurrentSessions int
	CurrentUser           string
}

// BuildVaultConfig builds a Vault from a vault-cli entry, or (Vault{}, false)
// when the entry has no path, has no tasks dir, or its tasks folder is absent on
// disk. It never returns an error for a skip.
//
// topics_dir is deliberately NOT a gate: it is optional in vault-cli, so a vault
// without it is still returned with an empty TopicsFolder.
func BuildVaultConfig(name string, cliVault *config.Vault, vaultName, vaultCLIPath string) (Vault, bool) {
	if cliVault == nil {
		return Vault{}, false
	}
	if cliVault.Path == "" {
		return Vault{}, false
	}
	if cliVault.TasksDir == "" {
		return Vault{}, false
	}
	if !isDir(filepath.Join(cliVault.Path, cliVault.TasksDir)) {
		return Vault{}, false
	}

	claudeScript := cliVault.ClaudeScript
	if claudeScript == "" {
		claudeScript = "claude"
	}

	return Vault{
		Name:              name,
		Path:              cliVault.Path,
		TasksFolder:       cliVault.TasksDir,
		GoalsFolder:       cliVault.GetGoalsDir(),
		VaultName:         vaultName,
		ClaudeScript:      claudeScript,
		VaultCLIPath:      vaultCLIPath,
		SessionProjectDir: cliVault.SessionProjectDir,
		TopicsFolder:      cliVault.TopicsDir,
	}, true
}

// ResolveDefaultConfigPath resolves the default config.yaml path XDG-first,
// falling back to the legacy repo-root path when only that exists.
func ResolveDefaultConfigPath(xdgPath, legacyPath string) string {
	if exists(xdgPath) {
		return xdgPath
	}
	if exists(legacyPath) {
		return legacyPath
	}
	return xdgPath
}

// Load reads configPath, merges it with the injected vault-cli loader's vaults and
// current user, and returns the resolved Config. An explicit non-empty `vaults:`
// block filters (a vault-cli vault not named stays off) and overrides the display
// name; an absent or empty block serves every vault-cli vault with a tasks folder
// that exists on disk. When nothing survives the merge it returns an error naming
// the reason.
func Load(ctx context.Context, loader config.Loader, configPath string) (*Config, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read config %q", configPath)
	}

	var parsed rawConfig
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return nil, errors.Wrapf(ctx, err, "parse config %q", configPath)
	}

	vaultCLIPath := defaultVaultCLIPath
	if parsed.VaultCLIPath != nil {
		vaultCLIPath = *parsed.VaultCLIPath
	}

	currentUser, err := loader.GetCurrentUser(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get current user")
	}

	cliVaults, err := loader.GetAllVaults(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get all vaults")
	}

	cliVaultByName := make(map[string]*config.Vault, len(cliVaults))
	for _, cliVault := range cliVaults {
		cliVaultByName[strings.ToLower(cliVault.Name)] = cliVault
	}

	vaults := resolveVaults(parsed, cliVaults, cliVaultByName, vaultCLIPath)
	if len(vaults) == 0 {
		return nil, errors.Errorf(
			ctx,
			"No vaults configured after merging with vault-cli output. "+
				"Every candidate was skipped: a vault-cli vault needs a tasks_dir "+
				"whose folder exists on disk, and an explicit config.yaml vaults "+
				"section must name vaults vault-cli knows. Check that vault-cli is "+
				"available and returns vaults with a tasks_dir.",
		)
	}

	maxSessions, err := coerceInt(ctx, parsed.MaxConcurrentSessions, 20)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "parse max_concurrent_sessions")
	}

	host := "127.0.0.1"
	if parsed.Host != nil {
		host = *parsed.Host
	}
	port := 8000
	if parsed.Port != nil {
		port = *parsed.Port
	}

	return &Config{
		Vaults:                vaults,
		Host:                  host,
		Port:                  port,
		MaxConcurrentSessions: maxSessions,
		CurrentUser:           currentUser,
	}, nil
}

// rawConfig is the parsed config.yaml. Pointer and any fields distinguish an
// absent key (nil) from a present-but-empty one, matching the Python
// `data.get(key, default)` semantics.
type rawConfig struct {
	VaultCLIPath          *string   `yaml:"vault_cli_path"`
	Host                  *string   `yaml:"host"`
	Port                  *int      `yaml:"port"`
	MaxConcurrentSessions any       `yaml:"max_concurrent_sessions"`
	Vaults                yaml.Node `yaml:"vaults"`
}

// override is one `vaults:` block entry, in document order.
type override struct {
	key   string
	value map[string]any
}

// resolveVaults applies the explicit-block/filter path or the serve-every-vault
// fallback. An explicit non-empty block keeps its role: it filters and overrides
// the display name. An absent or empty block serves every vault-cli vault.
func resolveVaults(
	parsed rawConfig,
	cliVaults []*config.Vault,
	cliVaultByName map[string]*config.Vault,
	vaultCLIPath string,
) []Vault {
	overrides := parseOverrides(parsed.Vaults)
	vaults := make([]Vault, 0, len(cliVaults))

	if len(overrides) > 0 {
		for _, entry := range overrides {
			cliVault := cliVaultByName[strings.ToLower(entry.key)]
			if cliVault == nil {
				continue
			}
			vaultName := overrideVaultName(entry, entry.key)
			vaultConfig, ok := BuildVaultConfig(entry.key, cliVault, vaultName, vaultCLIPath)
			if ok {
				vaults = append(vaults, vaultConfig)
			}
		}
		return vaults
	}

	for _, cliVault := range cliVaults {
		vaultConfig, ok := BuildVaultConfig(
			cliVault.Name,
			cliVault,
			titleCase(cliVault.Name),
			vaultCLIPath,
		)
		if ok {
			vaults = append(vaults, vaultConfig)
		}
	}
	return vaults
}

// parseOverrides extracts the `vaults:` block entries in document order. A
// missing or null block yields nil.
func parseOverrides(node yaml.Node) []override {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	overrides := make([]override, 0, len(node.Content)/2)
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		value := map[string]any{}
		// A null value (e.g. `personal:`) decodes to an empty map, mirroring
		// Python's `overrides or {}`.
		_ = node.Content[idx+1].Decode(&value)
		overrides = append(overrides, override{key: node.Content[idx].Value, value: value})
	}
	return overrides
}

// overrideVaultName returns the block's vault_name override, or the title-cased
// key when the override is absent or empty.
func overrideVaultName(entry override, key string) string {
	if raw, ok := entry.value["vault_name"]; ok {
		if name, ok := raw.(string); ok && name != "" {
			return name
		}
	}
	return titleCase(key)
}

// coerceInt converts a YAML scalar to an int, defaulting when absent. A string
// value is coerced, mirroring Python's int(...) on the parsed value.
func coerceInt(ctx context.Context, value any, fallback int) (int, error) {
	switch typed := value.(type) {
	case nil:
		return fallback, nil
	case int:
		return typed, nil
	case int64:
		return int(typed), nil
	case float64:
		return int(typed), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, errors.Wrapf(ctx, err, "coerce %q to int", typed)
		}
		return parsed, nil
	default:
		return 0, errors.Errorf(ctx, "unsupported max_concurrent_sessions type %T", value)
	}
}

// titleCase reproduces Python str.title(): the first letter of each word is
// upper-cased, the rest lower-cased, with any non-alphanumeric rune a word
// boundary.
func titleCase(value string) string {
	var builder strings.Builder
	atWordStart := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if atWordStart {
				builder.WriteRune(unicode.ToUpper(r))
			} else {
				builder.WriteRune(unicode.ToLower(r))
			}
			atWordStart = false
			continue
		}
		builder.WriteRune(r)
		atWordStart = true
	}
	return builder.String()
}

// isDir reports whether path is an existing directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
