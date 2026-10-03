// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sessionresolver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/sessionresolver"
)

// writeJSONL writes one JSON object per line to `<stem>.jsonl`.
func writeJSONL(directory, stem string, lines ...map[string]any) {
	Expect(os.MkdirAll(directory, 0o750)).To(Succeed())
	var builder strings.Builder
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		Expect(err).NotTo(HaveOccurred())
		builder.Write(encoded)
		builder.WriteString("\n")
	}
	Expect(os.WriteFile(filepath.Join(directory, stem+".jsonl"), []byte(builder.String()), 0o600)).To(Succeed())
}

func customTitle(title string) map[string]any {
	return map[string]any{"type": "custom-title", "customTitle": title}
}

var _ = Describe("SessionResolver", func() {
	var (
		ctx context.Context
		tmp string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmp = GinkgoT().TempDir()
	})

	DescribeTable("ResolveSessionID",
		func(setup func() (string, string), expectedID string, expectedOK bool) {
			displayName, projectDir := setup()
			id, ok := sessionresolver.ResolveSessionID(ctx, displayName, projectDir, map[string]string{})
			Expect(id).To(Equal(expectedID))
			Expect(ok).To(Equal(expectedOK))
		},
		Entry("last-custom-title-wins",
			func() (string, string) {
				stem := "abc12345-0000-0000-0000-000000000011"
				writeJSONL(tmp, stem,
					map[string]any{"type": "summary", "summary": "some summary"},
					customTitle("Old Task Name"),
					map[string]any{"type": "user", "message": "hello"},
					customTitle("New Task Name"),
				)
				return "New Task Name", tmp
			},
			"abc12345-0000-0000-0000-000000000011", true,
		),
		Entry("ambiguous-match-is-none",
			func() (string, string) {
				writeJSONL(tmp, "aaaaaaaa-0000-0000-0000-000000000001", customTitle("shared-title"))
				writeJSONL(tmp, "bbbbbbbb-0000-0000-0000-000000000001", customTitle("shared-title"))
				return "shared-title", tmp
			},
			"", false,
		),
	)

	It("does not resolve a session's old title once a newer title is set", func() {
		stem := "abc12345-0000-0000-0000-000000000011"
		writeJSONL(tmp, stem, customTitle("Old Task Name"), customTitle("New Task Name"))

		_, ok := sessionresolver.ResolveSessionID(ctx, "Old Task Name", tmp, nil)
		Expect(ok).To(BeFalse())
	})

	It("resolves an exact match", func() {
		stem := "abc12345-0000-0000-0000-000000000001"
		writeJSONL(tmp, stem,
			map[string]any{"type": "summary", "summary": "some summary"},
			customTitle("trading-alerts"),
		)

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("returns no match for a name no session carries", func() {
		writeJSONL(tmp, "abc12345-0000-0000-0000-000000000002", customTitle("other-session"))

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeFalse())
		Expect(id).To(BeEmpty())
	})

	It("returns no match for a missing project dir", func() {
		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", filepath.Join(tmp, "nonexistent"), nil)
		Expect(ok).To(BeFalse())
		Expect(id).To(BeEmpty())
	})

	It("skips a malformed JSON line", func() {
		stem := "abc12345-0000-0000-0000-000000000003"
		encoded, err := json.Marshal(customTitle("trading-alerts"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(
			filepath.Join(tmp, stem+".jsonl"),
			[]byte("this is not json\n"+string(encoded)+"\n"),
			0o600,
		)).To(Succeed())

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("skips an unreadable transcript", func() {
		Expect(os.MkdirAll(filepath.Join(tmp, "bad.jsonl"), 0o750)).To(Succeed())
		stem := "abc12345-0000-0000-0000-000000000005"
		writeJSONL(tmp, stem, customTitle("trading-alerts"))

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("only string-compares a path-traversal title", func() {
		stem := "abc12345-0000-0000-0000-000000000006"
		writeJSONL(tmp, stem, customTitle("../../etc/passwd"))

		id, ok := sessionresolver.ResolveSessionID(ctx, "../../etc/passwd", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("does not let a keyless trailing custom-title erase the previous title", func() {
		stem := "abc12345-0000-0000-0000-000000000013"
		writeJSONL(tmp, stem, customTitle("Real Title"), map[string]any{"type": "custom-title"})

		id, ok := sessionresolver.ResolveSessionID(ctx, "Real Title", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("tolerates an over-long line", func() {
		stem := "abc12345-0000-0000-0000-000000000007"
		encoded, err := json.Marshal(customTitle("trading-alerts"))
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(
			filepath.Join(tmp, stem+".jsonl"),
			[]byte(strings.Repeat("x", 5000)+"\n"+string(encoded)+"\n"),
			0o600,
		)).To(Succeed())

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("tolerates extra JSON fields", func() {
		stem := "abc12345-0000-0000-0000-000000000008"
		writeJSONL(tmp, stem, map[string]any{
			"type":        "custom-title",
			"customTitle": "trading-alerts",
			"timestamp":   1234567890,
			"extra":       "ignored",
		})

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(stem))
	})

	It("yields no match when the customTitle key is missing", func() {
		writeJSONL(tmp, "abc12345-0000-0000-0000-000000000009", map[string]any{"type": "custom-title"})

		id, ok := sessionresolver.ResolveSessionID(ctx, "trading-alerts", tmp, nil)
		Expect(ok).To(BeFalse())
		Expect(id).To(BeEmpty())
	})

	It("prefers a live process over ambiguous transcripts", func() {
		writeJSONL(tmp, "aaaaaaaa-0000-0000-0000-000000000001", customTitle("BRO-21903 Check Builds"))
		writeJSONL(tmp, "bbbbbbbb-0000-0000-0000-000000000001", customTitle("BRO-21903 Check Builds"))
		liveStem := "0bc9bb57-7034-49b5-b73c-70fe0682e953"

		id, ok := sessionresolver.ResolveSessionID(
			ctx, "BRO-21903 Check Builds", tmp,
			map[string]string{"BRO-21903 Check Builds": liveStem},
		)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal(liveStem))
	})

	Describe("IsUUID", func() {
		It("accepts UUID-formatted values", func() {
			Expect(sessionresolver.IsUUID("550e8400-e29b-41d4-a716-446655440000")).To(BeTrue())
			Expect(sessionresolver.IsUUID("00000000-0000-0000-0000-000000000000")).To(BeTrue())
			Expect(sessionresolver.IsUUID("FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF")).To(BeTrue())
		})

		It("rejects non-UUID values", func() {
			Expect(sessionresolver.IsUUID("trading-alerts")).To(BeFalse())
			Expect(sessionresolver.IsUUID("")).To(BeFalse())
			Expect(sessionresolver.IsUUID("550e8400-e29b-41d4-a716")).To(BeFalse())
			Expect(sessionresolver.IsUUID("550e8400-e29b-41d4-a716-4466554400001")).To(BeFalse())
			Expect(sessionresolver.IsUUID("zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz")).To(BeFalse())
		})
	})
})
