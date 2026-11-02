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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-ui/pkg/sessionresolver"
)

// maxLineBytes mirrors the resolver's unexported limit.
const maxLineBytes = 4096

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

// writeRaw writes content verbatim to `<stem>.jsonl` and returns its path.
func writeRaw(directory, stem, content string) string {
	Expect(os.MkdirAll(directory, 0o750)).To(Succeed())
	path := filepath.Join(directory, stem+".jsonl")
	Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	return path
}

// appendRaw appends content verbatim to path.
func appendRaw(path, content string) {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	Expect(err).NotTo(HaveOccurred())
	_, err = fh.WriteString(content)
	Expect(err).NotTo(HaveOccurred())
	Expect(fh.Close()).To(Succeed())
}

// titleLine returns the JSON encoding of a custom-title line, without newline.
func titleLine(title string) string {
	encoded, err := json.Marshal(customTitle(title))
	Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

// paddedTitleLine returns a valid custom-title line of exactly total bytes.
func paddedTitleLine(total int) string {
	head := `{"type":"custom-title","customTitle":"`
	tail := `"}`
	return head + strings.Repeat("a", total-len(head)-len(tail)) + tail
}

// lineTitle decodes the customTitle of a padded line.
func lineTitle(line string) string {
	var parsed map[string]any
	Expect(json.Unmarshal([]byte(line), &parsed)).To(Succeed())
	title, ok := parsed["customTitle"].(string)
	Expect(ok).To(BeTrue())
	return title
}

// resolveFresh resolves, then clears the cache and resolves again, asserting the
// cached answer equals the fresh full-scan answer. It returns the cached answer.
func resolveFresh(ctx context.Context, displayName, projectDir string) (string, bool) {
	id, ok := sessionresolver.ResolveSessionID(ctx, displayName, projectDir, nil)
	sessionresolver.ResetCacheForTest()
	freshID, freshOK := sessionresolver.ResolveSessionID(ctx, displayName, projectDir, nil)
	Expect(freshID).To(Equal(id))
	Expect(freshOK).To(Equal(ok))
	return id, ok
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

	Describe("transcript cache", func() {
		BeforeEach(func() {
			sessionresolver.ResetCacheForTest()
		})

		It("sees a title appended after a first resolve", func() {
			stem := "abc12345-0000-0000-0000-000000000021"
			path := writeRaw(tmp, stem, titleLine("First Title")+"\n")

			id, ok := resolveFresh(ctx, "First Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			appendRaw(path, titleLine("Second Title")+"\n")

			id, ok = resolveFresh(ctx, "Second Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "First Title", tmp)
			Expect(ok).To(BeFalse())
		})

		It("resets after a rewrite to a shorter body", func() {
			stem := "abc12345-0000-0000-0000-000000000022"
			writeRaw(tmp, stem, titleLine("First Title With A Long Name")+"\n"+titleLine("Second Title")+"\n")

			_, ok := resolveFresh(ctx, "Second Title", tmp)
			Expect(ok).To(BeTrue())

			writeRaw(tmp, stem, titleLine("Short")+"\n")

			id, ok := resolveFresh(ctx, "Short", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "Second Title", tmp)
			Expect(ok).To(BeFalse())
		})

		It("resets after an in-place rewrite to a longer body", func() {
			stem := "abc12345-0000-0000-0000-000000000023"
			writeRaw(tmp, stem, titleLine("Old")+"\n")

			_, ok := resolveFresh(ctx, "Old", tmp)
			Expect(ok).To(BeTrue())

			writeRaw(tmp, stem, titleLine("A much longer replacement title")+"\n")

			id, ok := resolveFresh(ctx, "A much longer replacement title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "Old", tmp)
			Expect(ok).To(BeFalse())
		})

		It("resets after a delete and recreate", func() {
			stem := "abc12345-0000-0000-0000-000000000024"
			path := writeRaw(tmp, stem, titleLine("First Title")+"\n")

			_, ok := resolveFresh(ctx, "First Title", tmp)
			Expect(ok).To(BeTrue())

			Expect(os.Remove(path)).To(Succeed())
			writeRaw(tmp, stem, titleLine("Second Title")+"\n")

			id, ok := resolveFresh(ctx, "Second Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "First Title", tmp)
			Expect(ok).To(BeFalse())
		})

		It("applies an unterminated last line and then reconciles it after an append", func() {
			stem := "abc12345-0000-0000-0000-000000000025"
			path := writeRaw(
				tmp,
				stem,
				titleLine("Committed Title")+"\n"+`{"type":"custom-title","customTitle":"Partial Title"}`,
			)

			id, ok := resolveFresh(ctx, "Partial Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "Committed Title", tmp)
			Expect(ok).To(BeFalse())

			appendRaw(path, "garbage\n")

			id, ok = resolveFresh(ctx, "Committed Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = resolveFresh(ctx, "Partial Title", tmp)
			Expect(ok).To(BeFalse())
		})

		It("honours the maxLineBytes boundaries", func() {
			parsed := paddedTitleLine(maxLineBytes - 1)
			parsedStem := "abc12345-0000-0000-0000-000000000026"
			writeRaw(tmp, parsedStem, parsed+"\n")
			id, ok := resolveFresh(ctx, lineTitle(parsed), tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(parsedStem))

			skipped := paddedTitleLine(maxLineBytes)
			skippedStem := "abc12345-0000-0000-0000-000000000027"
			writeRaw(tmp, skippedStem, skipped+"\n")
			_, ok = resolveFresh(ctx, lineTitle(skipped), tmp)
			Expect(ok).To(BeFalse())

			unterminated := paddedTitleLine(maxLineBytes)
			unterminatedStem := "abc12345-0000-0000-0000-000000000028"
			writeRaw(tmp, unterminatedStem, unterminated)
			id, ok = resolveFresh(ctx, lineTitle(unterminated), tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(unterminatedStem))
		})

		It("skips an over-long line containing custom-title and reads the next line", func() {
			stem := "abc12345-0000-0000-0000-000000000029"
			overlong := strings.Repeat("x", 100) + `"custom-title"` + strings.Repeat("x", 5000)
			writeRaw(tmp, stem, overlong+"\n"+titleLine("After Overlong")+"\n")

			id, ok := resolveFresh(ctx, "After Overlong", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))
		})

		It("skips a malformed JSON line containing custom-title", func() {
			stem := "abc12345-0000-0000-0000-000000000030"
			writeRaw(tmp, stem, `{"type":"custom-title" broken`+"\n"+titleLine("After Malformed")+"\n")

			id, ok := resolveFresh(ctx, "After Malformed", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))
		})

		It("does not let a non-string customTitle resolve the earlier title", func() {
			stem := "abc12345-0000-0000-0000-000000000031"
			writeRaw(
				tmp,
				stem,
				titleLine("Earlier Title")+"\n"+`{"type":"custom-title","customTitle":123}`+"\n",
			)

			_, ok := resolveFresh(ctx, "Earlier Title", tmp)
			Expect(ok).To(BeFalse())
		})

		It("evicts a deleted transcript", func() {
			stem := "abc12345-0000-0000-0000-000000000032"
			path := writeRaw(tmp, stem, titleLine("Only Here")+"\n")

			_, ok := resolveFresh(ctx, "Only Here", tmp)
			Expect(ok).To(BeTrue())

			Expect(os.Remove(path)).To(Succeed())

			_, ok = resolveFresh(ctx, "Only Here", tmp)
			Expect(ok).To(BeFalse())
		})

		It("skips a transcript whose stat fails", func() {
			stem := "abc12345-0000-0000-0000-000000000034"
			writeRaw(tmp, stem, titleLine("Real Title")+"\n")
			Expect(os.Symlink(
				filepath.Join(tmp, "does-not-exist"),
				filepath.Join(tmp, "broken.jsonl"),
			)).To(Succeed())

			id, ok := resolveFresh(ctx, "Real Title", tmp)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))
		})

		It("returns no match for an empty transcript", func() {
			writeRaw(tmp, "abc12345-0000-0000-0000-000000000035", "")

			_, ok := resolveFresh(ctx, "anything", tmp)
			Expect(ok).To(BeFalse())
		})

		It("stops when the context is already cancelled", func() {
			writeRaw(tmp, "abc12345-0000-0000-0000-000000000036", titleLine("Some Title")+"\n")
			cancelled, cancel := context.WithCancel(ctx)
			cancel()

			id, ok := sessionresolver.ResolveSessionID(cancelled, "Some Title", tmp, nil)
			Expect(ok).To(BeFalse())
			Expect(id).To(BeEmpty())
		})

		It("returns the cached result without reopening an unchanged file", func() {
			stem := "abc12345-0000-0000-0000-000000000033"
			path := writeRaw(tmp, stem, titleLine("OriginalTitle")+"\n")
			fixed := time.Unix(1700000000, 0)
			Expect(os.Chtimes(path, fixed, fixed)).To(Succeed())

			id, ok := sessionresolver.ResolveSessionID(ctx, "OriginalTitle", tmp, nil)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			writeRaw(tmp, stem, titleLine("ReplacedTitle")+"\n")
			Expect(os.Chtimes(path, fixed, fixed)).To(Succeed())

			// Same inode, size and mtime: the cached answer must win without
			// reading the file. This is the documented accepted limitation.
			id, ok = sessionresolver.ResolveSessionID(ctx, "OriginalTitle", tmp, nil)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal(stem))

			_, ok = sessionresolver.ResolveSessionID(ctx, "ReplacedTitle", tmp, nil)
			Expect(ok).To(BeFalse())
		})
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
