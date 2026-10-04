// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultconfig_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultcliconfig "github.com/bborbe/vault-cli/pkg/config"

	"github.com/bborbe/vault-ui/pkg/vaultconfig"
)

// fakeLoader is a config.Loader backed by an in-memory vault list.
type fakeLoader struct {
	vaults      []*vaultcliconfig.Vault
	currentUser string
}

func (f *fakeLoader) Load(ctx context.Context) (*vaultcliconfig.Config, error) {
	return &vaultcliconfig.Config{}, nil
}

func (f *fakeLoader) GetVaultPath(ctx context.Context, vaultName string) (string, error) {
	return "", nil
}

func (f *fakeLoader) GetVault(ctx context.Context, vaultName string) (*vaultcliconfig.Vault, error) {
	return nil, nil
}

func (f *fakeLoader) GetAllVaults(ctx context.Context) ([]*vaultcliconfig.Vault, error) {
	return f.vaults, nil
}

func (f *fakeLoader) GetCurrentUser(ctx context.Context) (string, error) {
	return f.currentUser, nil
}

// healthyVault creates a real vault directory with a "24 Tasks" folder and
// returns its vault-cli entry.
func healthyVault(root, name string) *vaultcliconfig.Vault {
	ExpectWithOffset(1, os.MkdirAll(filepath.Join(root, name, "24 Tasks"), 0o750)).To(Succeed())
	return &vaultcliconfig.Vault{Name: name, Path: filepath.Join(root, name), TasksDir: "24 Tasks"}
}

// writeConfig writes a config.yaml with the given body and returns its path.
func writeConfig(root, body string) string {
	path := filepath.Join(root, "config.yaml")
	ExpectWithOffset(1, os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
	return path
}

// load runs Load with the given config body and vault-cli vaults.
func load(ctx context.Context, root, body string, loader *fakeLoader) (*vaultconfig.Config, error) {
	return vaultconfig.Load(ctx, loader, writeConfig(root, body))
}

var _ = Describe("ConfigMerge", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("ConfigMerge",
		func(body func(root string) (*vaultconfig.Config, error)) {
			body(GinkgoT().TempDir())
		},
		Entry("absent-tasks-folder-skips-vault", func(root string) (*vaultconfig.Config, error) {
			healthy := healthyVault(root, "personal")
			ghost := &vaultcliconfig.Vault{
				Name:     "ghost",
				Path:     filepath.Join(root, "ghost"),
				TasksDir: "24 Tasks",
			}
			Expect(os.MkdirAll(filepath.Join(root, "ghost"), 0o750)).To(Succeed())
			loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthy, ghost}}

			config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Vaults).To(HaveLen(1))
			Expect(config.Vaults[0].Name).To(Equal("personal"))
			return config, nil
		}),
		Entry("no-tasks-dir-skips-vault", func(root string) (*vaultconfig.Config, error) {
			healthy := healthyVault(root, "personal")
			// A real Tasks/ folder exists on disk, but the entry has no tasks_dir.
			taskless := &vaultcliconfig.Vault{Name: "trading", Path: filepath.Join(root, "trading")}
			Expect(os.MkdirAll(filepath.Join(root, "trading", "Tasks"), 0o750)).To(Succeed())
			loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthy, taskless}}

			config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Vaults).To(HaveLen(1))
			Expect(config.Vaults[0].Name).To(Equal("personal"))
			return config, nil
		}),
	)

	It("reads a healthy vault", func() {
		root := GinkgoT().TempDir()
		healthy := healthyVault(root, "personal")
		healthy.ClaudeScript = "claude-personal.sh"
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthy}}

		config, err := load(ctx, root, "vaults:\n  personal:\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults).To(HaveLen(1))
		vault := config.Vaults[0]
		Expect(vault.Name).To(Equal("personal"))
		Expect(vault.Path).To(Equal(filepath.Join(root, "personal")))
		Expect(vault.VaultName).To(Equal("Personal"))
		Expect(vault.TasksFolder).To(Equal("24 Tasks"))
		Expect(vault.ClaudeScript).To(Equal("claude-personal.sh"))
	})

	It("falls back to claude when claude_script is absent or empty", func() {
		root := GinkgoT().TempDir()
		absent := healthyVault(root, "absent")
		empty := healthyVault(root, "empty")
		empty.ClaudeScript = ""
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{absent, empty}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults).To(HaveLen(2))
		Expect(config.Vaults[0].ClaudeScript).To(Equal("claude"))
		Expect(config.Vaults[1].ClaudeScript).To(Equal("claude"))
	})

	It("reads multiple vaults in vault-cli order", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{
			healthyVault(root, "personal"),
			healthyVault(root, "work"),
		}}

		config, err := load(ctx, root, "vaults:\n  personal: {}\n  work: {}\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults).To(HaveLen(2))
		Expect(config.Vaults[0].Name).To(Equal("personal"))
		Expect(config.Vaults[1].Name).To(Equal("work"))
	})

	It("uses host, port and max_concurrent_sessions defaults", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		config, err := load(ctx, root, "vaults:\n  personal: {}\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Host).To(Equal("127.0.0.1"))
		Expect(config.Port).To(Equal(8000))
		Expect(config.MaxConcurrentSessions).To(Equal(20))
	})

	It("respects host, port and max_concurrent_sessions overrides", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		config, err := load(
			ctx,
			root,
			"vaults:\n  personal: {}\nhost: 0.0.0.0\nport: 9000\nmax_concurrent_sessions: 5\n",
			loader,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Host).To(Equal("0.0.0.0"))
		Expect(config.Port).To(Equal(9000))
		Expect(config.MaxConcurrentSessions).To(Equal(5))
	})

	It("coerces a string max_concurrent_sessions to int", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		config, err := load(
			ctx,
			root,
			"vaults:\n  personal: {}\nmax_concurrent_sessions: \"5\"\n",
			loader,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.MaxConcurrentSessions).To(Equal(5))
	})

	It("populates current_user from the loader", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{
			vaults:      []*vaultcliconfig.Vault{healthyVault(root, "personal")},
			currentUser: "alice",
		}

		config, err := load(ctx, root, "vaults:\n  personal: {}\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.CurrentUser).To(Equal("alice"))
	})

	It("populates session_project_dir when present and defaults to empty when absent", func() {
		root := GinkgoT().TempDir()
		present := healthyVault(root, "present")
		present.SessionProjectDir = "/home/me/.claude/projects/-personal"
		absent := healthyVault(root, "absent")
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{present, absent}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults[0].SessionProjectDir).To(Equal("/home/me/.claude/projects/-personal"))
		Expect(config.Vaults[1].SessionProjectDir).To(Equal(""))
	})

	It("keeps topics_dir even when its folder is absent", func() {
		root := GinkgoT().TempDir()
		vault := healthyVault(root, "personal")
		vault.TopicsDir = "23 Topics"
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{vault}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults).To(HaveLen(1))
		Expect(config.Vaults[0].TopicsFolder).To(Equal("23 Topics"))
	})

	It("keeps a vault without topics_dir with an empty TopicsFolder", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Vaults).To(HaveLen(1))
		Expect(config.Vaults[0].TopicsFolder).To(Equal(""))
	})

	It("serves every vault-cli vault when the vaults block is absent", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{
			healthyVault(root, "personal"),
			healthyVault(root, "work"),
			healthyVault(root, "family"),
		}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(vaultNames(config)).To(Equal([]string{"personal", "work", "family"}))
	})

	It("serves every vault-cli vault when the vaults block is empty", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{
			healthyVault(root, "personal"),
			healthyVault(root, "work"),
		}}

		config, err := load(ctx, root, "vaults: {}\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(vaultNames(config)).To(Equal([]string{"personal", "work"}))
	})

	It("filters to the explicit block and overrides vault_name", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{
			healthyVault(root, "personal"),
			healthyVault(root, "work"),
			healthyVault(root, "family"),
		}}

		config, err := load(ctx, root, "vaults:\n  personal:\n    vault_name: My Vault\n  work:\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(vaultNames(config)).To(Equal([]string{"personal", "work"}))
		Expect(config.Vaults[0].VaultName).To(Equal("My Vault"))
		Expect(config.Vaults[1].VaultName).To(Equal("Work"))
	})

	It("skips a vault-cli entry without a path", func() {
		root := GinkgoT().TempDir()
		pathless := &vaultcliconfig.Vault{Name: "nopath", TasksDir: "24 Tasks"}
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{
			healthyVault(root, "personal"),
			pathless,
		}}

		config, err := load(ctx, root, "host: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())
		Expect(vaultNames(config)).To(Equal([]string{"personal"}))
	})

	It("carries a non-default vault_cli_path through both construction paths", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		fromFallback, err := load(ctx, root, "vault_cli_path: /opt/vault-cli\nhost: 127.0.0.1\n", loader)
		Expect(err).NotTo(HaveOccurred())

		explicitPath := writeConfig(root, "vault_cli_path: /opt/vault-cli\nvaults:\n  personal:\n")
		fromExplicit, err := vaultconfig.Load(ctx, loader, explicitPath)
		Expect(err).NotTo(HaveOccurred())

		Expect(fromFallback.Vaults).To(Equal(fromExplicit.Vaults))
		Expect(fromFallback.Vaults[0].VaultCLIPath).To(Equal("/opt/vault-cli"))
	})

	It("errors when every vault was skipped", func() {
		root := GinkgoT().TempDir()
		taskless := &vaultcliconfig.Vault{Name: "trading", Path: filepath.Join(root, "trading")}
		Expect(os.MkdirAll(filepath.Join(root, "trading"), 0o750)).To(Succeed())
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{taskless}}

		_, err := load(ctx, root, "vaults:\n  trading:\n", loader)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("No vaults configured"))
	})

	It("errors when the config file does not exist", func() {
		loader := &fakeLoader{}
		_, err := vaultconfig.Load(ctx, loader, filepath.Join(GinkgoT().TempDir(), "missing.yaml"))
		Expect(err).To(HaveOccurred())
	})

	It("errors when the config file is malformed YAML", func() {
		root := GinkgoT().TempDir()
		loader := &fakeLoader{vaults: []*vaultcliconfig.Vault{healthyVault(root, "personal")}}

		_, err := load(ctx, root, "vaults: [unclosed\n", loader)
		Expect(err).To(HaveOccurred())
	})

	Describe("ResolveDefaultConfigPath", func() {
		It("returns the XDG path when it exists", func() {
			root := GinkgoT().TempDir()
			xdg := writeConfig(root, "host: 127.0.0.1\n")
			legacy := filepath.Join(root, "legacy", "config.yaml")

			Expect(vaultconfig.ResolveDefaultConfigPath(xdg, legacy)).To(Equal(xdg))
		})

		It("falls back to the legacy path when only that exists", func() {
			root := GinkgoT().TempDir()
			legacyDir := filepath.Join(root, "legacy")
			Expect(os.MkdirAll(legacyDir, 0o750)).To(Succeed())
			legacy := writeConfig(legacyDir, "host: 127.0.0.1\n")
			xdg := filepath.Join(root, "xdg", "config.yaml")

			Expect(vaultconfig.ResolveDefaultConfigPath(xdg, legacy)).To(Equal(legacy))
		})

		It("returns the XDG path when neither exists", func() {
			root := GinkgoT().TempDir()
			xdg := filepath.Join(root, "xdg", "config.yaml")
			legacy := filepath.Join(root, "legacy", "config.yaml")

			Expect(vaultconfig.ResolveDefaultConfigPath(xdg, legacy)).To(Equal(xdg))
		})

		It("prefers the XDG path when both exist", func() {
			root := GinkgoT().TempDir()
			xdg := writeConfig(root, "host: 127.0.0.1\n")
			legacyDir := filepath.Join(root, "legacy")
			Expect(os.MkdirAll(legacyDir, 0o750)).To(Succeed())
			legacy := writeConfig(legacyDir, "host: 127.0.0.1\n")

			Expect(vaultconfig.ResolveDefaultConfigPath(xdg, legacy)).To(Equal(xdg))
		})
	})
})

// vaultNames returns the resolved vault names in order.
func vaultNames(config *vaultconfig.Config) []string {
	result := make([]string, 0, len(config.Vaults))
	for _, vault := range config.Vaults {
		result = append(result, vault.Name)
	}
	return result
}
