// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/bborbe/vault-ui/pkg/pageindex"
)

// allocationFileCount is the folder size the allocation measurement uses. It is
// large enough that a per-page structure dwarfs the listing overhead.
const allocationFileCount = 5000

// writeAllocationVault builds a real temp vault holding count parseable pages
// and returns the vault root and the pages folder.
func writeAllocationVault(t *testing.T, count int) (vaultDir, folderDir string) {
	t.Helper()
	vaultDir = t.TempDir()
	folderDir = filepath.Join(vaultDir, equivalenceFolder)
	if err := os.MkdirAll(folderDir, 0750); err != nil {
		t.Fatalf("mkdir %s: %v", folderDir, err)
	}
	for i := 0; i < count; i++ {
		writeAllocationFile(t, folderDir, i, false)
	}
	return vaultDir, folderDir
}

// writeAllocationFile writes one page file. A changed file's content differs in
// byte count, so its fingerprint moves.
func writeAllocationFile(t *testing.T, folderDir string, i int, changed bool) {
	t.Helper()
	suffix := ""
	if changed {
		suffix = " changed"
	}
	name := fmt.Sprintf("Page%05d.md", i)
	content := fmt.Sprintf("---\ntitle: Page%05d\n---\n# Page%05d%s\n", i, i, suffix)
	if err := os.WriteFile(filepath.Join(folderDir, name), []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// measureAllocation reports the TotalAlloc delta around fn.
func measureAllocation(fn func()) uint64 {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestUnchangedRescanAllocation is a plain test, not a Ginkgo spec: the spec's
// verification runs it with -run TestUnchangedRescanAllocation, which does not
// run TestPageIndex and therefore does not register Ginkgo's fail handler.
func TestUnchangedRescanAllocation(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("allocation measurement is only meaningful without -race")
	}
	ctx := context.Background()
	vaultDir, folderDir := writeAllocationVault(t, allocationFileCount)
	ri := newRealIndex()
	key := pageindex.NewKey(vaultDir, equivalenceFolder)
	if _, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder); err != nil {
		t.Fatalf("warm list: %v", err)
	}

	listingBytes := measureAllocation(func() {
		if _, err := ri.lister.ListFiles(ctx, vaultDir, equivalenceFolder); err != nil {
			t.Fatalf("listing: %v", err)
		}
	})
	if listingBytes == 0 {
		t.Fatal("listing allocated nothing; the measurement is invalid")
	}

	ri.reader.reset()
	fpBefore := pageindex.FingerprintSetIdentity(ri.index, key)
	snapBefore := pageindex.SnapshotIdentity(ri.index, key)
	passBytes := measureAllocation(func() {
		if err := ri.index.Refresh(ctx, key); err != nil {
			t.Fatalf("unchanged refresh: %v", err)
		}
	})
	fpAfter := pageindex.FingerprintSetIdentity(ri.index, key)
	snapAfter := pageindex.SnapshotIdentity(ri.index, key)

	ratio := float64(passBytes) / float64(listingBytes)
	t.Logf("listing bytes=%d", listingBytes)
	t.Logf("unchanged pass bytes=%d ratio=%.3f", passBytes, ratio)
	t.Logf("fingerprint identity before=%q after=%q", fpBefore, fpAfter)
	t.Logf("snapshot identity before=%q after=%q", snapBefore, snapAfter)
	t.Logf("unchanged pass reads=%v", ri.reader.Names())

	if ratio > 1.35 {
		t.Errorf("unchanged pass ratio %.3f exceeds 1.35", ratio)
	}
	if passBytes > 5_500_000 {
		t.Errorf("unchanged pass allocated %d bytes, exceeds 5.5 MB", passBytes)
	}
	for label, value := range map[string]string{
		"fingerprint before": fpBefore,
		"fingerprint after":  fpAfter,
		"snapshot before":    snapBefore,
		"snapshot after":     snapAfter,
	} {
		if value == "" {
			t.Fatalf("%s identity is empty; the key lookup missed", label)
		}
	}
	if fpBefore != fpAfter {
		t.Errorf("unchanged pass replaced the fingerprint set: %q -> %q", fpBefore, fpAfter)
	}
	if snapBefore != snapAfter {
		t.Errorf("unchanged pass replaced the snapshot: %q -> %q", snapBefore, snapAfter)
	}

	// A pass that changes 10 of the 5000 files re-reads exactly those 10 and
	// allocates in proportion to them.
	ri.reader.reset()
	settle()
	const changedCount = 10
	wantNames := make([]string, 0, changedCount)
	for i := 0; i < changedCount; i++ {
		writeAllocationFile(t, folderDir, i, true)
		wantNames = append(wantNames, fmt.Sprintf("Page%05d.md", i))
	}
	changedBytes := measureAllocation(func() {
		if err := ri.index.Refresh(ctx, key); err != nil {
			t.Fatalf("changed refresh: %v", err)
		}
	})
	changedRatio := float64(changedBytes) / float64(listingBytes)
	t.Logf("changed pass bytes=%d ratio=%.3f", changedBytes, changedRatio)
	t.Logf("changed pass reads=%v", ri.reader.Names())
	if got := ri.reader.Names(); !reflect.DeepEqual(got, wantNames) {
		t.Errorf("changed pass read %v, want %v", got, wantNames)
	}
	if changedRatio > 1.5 {
		t.Errorf("changed pass ratio %.3f exceeds 1.5", changedRatio)
	}

	// An emptied folder lists as nil and equals the empty recorded set, so the
	// second pass over it is an unchanged pass.
	if err := os.RemoveAll(folderDir); err != nil {
		t.Fatalf("remove folder: %v", err)
	}
	if err := ri.index.Refresh(ctx, key); err != nil {
		t.Fatalf("empty refresh: %v", err)
	}
	ri.reader.reset()
	fpBefore = pageindex.FingerprintSetIdentity(ri.index, key)
	snapBefore = pageindex.SnapshotIdentity(ri.index, key)
	if err := ri.index.Refresh(ctx, key); err != nil {
		t.Fatalf("second empty refresh: %v", err)
	}
	fpAfter = pageindex.FingerprintSetIdentity(ri.index, key)
	snapAfter = pageindex.SnapshotIdentity(ri.index, key)
	t.Logf("empty folder fingerprint identity %q -> %q", fpBefore, fpAfter)
	t.Logf("empty folder snapshot identity %q -> %q", snapBefore, snapAfter)
	if fpBefore != fpAfter || snapBefore != snapAfter {
		t.Errorf(
			"empty-folder unchanged pass replaced identity: fingerprint %q -> %q, snapshot %q -> %q",
			fpBefore, fpAfter, snapBefore, snapAfter,
		)
	}
	if got := ri.reader.Names(); len(got) != 0 {
		t.Errorf("empty-folder unchanged pass read %v, want none", got)
	}
}

// TestWriteMarkSkipsUnchangedFastPath proves a pending write mark defeats the
// unchanged fast path: the marked file is re-read even though no fingerprint
// changed anywhere.
func TestWriteMarkSkipsUnchangedFastPath(t *testing.T) {
	ctx := context.Background()
	vaultDir := t.TempDir()
	folderDir := filepath.Join(vaultDir, equivalenceFolder)
	if err := os.MkdirAll(folderDir, 0750); err != nil {
		t.Fatalf("mkdir %s: %v", folderDir, err)
	}
	for _, name := range []string{"Alpha.md", "Beta.md"} {
		title := name[:len(name)-len(".md")]
		content := fmt.Sprintf("---\ntitle: %s\n---\n# %s\n", title, title)
		if err := os.WriteFile(filepath.Join(folderDir, name), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ri := newRealIndex()
	key := pageindex.NewKey(vaultDir, equivalenceFolder)
	if _, err := ri.index.ListPages(ctx, vaultDir, equivalenceFolder); err != nil {
		t.Fatalf("warm list: %v", err)
	}

	ri.reader.reset()
	ri.index.MarkFileDirty(key, "Alpha")
	if err := ri.index.Refresh(ctx, key); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := ri.reader.Names(); !reflect.DeepEqual(got, []string{"Alpha.md"}) {
		t.Fatalf("write-marked pass read %v, want [Alpha.md]", got)
	}
}
