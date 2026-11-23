// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package board

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/vaultapi"
)

type pendingTestGeneration struct{}

func (pendingTestGeneration) Generation() uint64 { return 1 }

var _ = Describe("taskSnapshotStore with a pending write", func() {
	It("rebuilds the cached snapshot so a recorded write shows before vault-api catches up", func() {
		ctx := context.Background()
		vault := Vault{
			Name: "vault-a", VaultName: "vault-a", Path: "/vault-a", TasksFolder: "25 Tasks",
		}
		source := vaultapi.NewSourceFromData(
			[]vaultapi.Task{{Title: "T", Status: "in_progress", Vault: "vault-a", Flag: true}},
			nil, nil,
		)
		list := vaultapi.NewListOperation(source, vault.VaultName, vault.TasksFolder, "", "")
		store := newTaskSnapshotStore(taskSnapshotParams{
			Build: func(ctx context.Context, v Vault) ([]taskSnapshotRow, error) {
				items, err := list.Execute(ctx, "", v.VaultName, v.TasksFolder, nil, true, "", "")
				if err != nil {
					return nil, err
				}
				rows := make([]taskSnapshotRow, 0, len(items))
				for _, item := range items {
					rows = append(rows, taskSnapshotRow{item: item, vault: v})
				}
				return rows, nil
			},
			Revisions:   source,
			Generations: pendingTestGeneration{},
		})

		rows, err := store.List(ctx, vault)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].item.Flag).To(BeTrue())

		source.RecordPending("vault-a", vaultapi.KindTask, "T", vaultapi.PendingFields{
			Flag: func() *bool { b := false; return &b }(),
		})
		Eventually(func() bool {
			rows, err := store.List(ctx, vault)
			Expect(err).NotTo(HaveOccurred())
			return rows[0].item.Flag
		}).Should(BeFalse())
	})
})
