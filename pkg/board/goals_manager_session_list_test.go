// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vault-cli list read model", func() {
	It("populates ManagerSessionID for a goal page that carries it", func() {
		const managerSessionID = "33333333-3333-3333-3333-333333333333"
		vaultPath := GinkgoT().TempDir()
		goalsDir := filepath.Join(vaultPath, "24 Goals")
		Expect(os.MkdirAll(goalsDir, 0o750)).To(Succeed())

		content := "---\nstatus: in_progress\nmanager_session_id: " +
			managerSessionID + "\n---\n# Goal\n"
		Expect(os.WriteFile(
			filepath.Join(goalsDir, "Goal.md"), []byte(content), 0o600,
		)).To(Succeed())

		items, err := ops.NewListOperation(storage.NewPageStorage(nil)).Execute(
			context.Background(), vaultPath, "personal", "24 Goals", nil, true, "", "",
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(items).To(HaveLen(1))
		Expect(items[0].Name).To(Equal("Goal"))
		Expect(items[0].ManagerSessionID).To(Equal(managerSessionID))
	})
})
