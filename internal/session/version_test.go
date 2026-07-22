package session

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"
)

// createLinkedSession is a test helper that creates a session, attaches the
// given metadata, and links it to a chain. It returns the session id, the
// assigned chain id, and the assigned version number.
func createLinkedSession(t *testing.T, store *Store, entryFile, project, category, ownerID string, tags ...string) (sessionID, chainID string, versionNo int) {
	t.Helper()

	sess, err := store.CreateUploaded(entryFile, entryFile)
	if err != nil {
		t.Fatalf("CreateUploaded(%q): %v", entryFile, err)
	}
	if len(tags) > 0 {
		if err := store.AddTags(sess.ID, tags...); err != nil {
			t.Fatalf("AddTags: %v", err)
		}
	}
	if category != "" {
		if err := store.SetCategory(sess.ID, category); err != nil {
			t.Fatalf("SetCategory: %v", err)
		}
	}
	if project != "" {
		if err := store.SetProject(sess.ID, project); err != nil {
			t.Fatalf("SetProject: %v", err)
		}
	}
	cid, vno, err := store.LinkToChain(sess.ID, project, entryFile, ownerID)
	if err != nil {
		t.Fatalf("LinkToChain: %v", err)
	}
	return sess.ID, cid, vno
}

func TestLinkToChain_CreatesNewChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	sid, cid, vno := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a", "b")

	if cid == "" {
		t.Fatal("expected non-empty chain id for first session in chain")
	}
	if vno != 1 {
		t.Fatalf("first session should be v1, got v%d", vno)
	}

	info, err := store.getChainInfo(cid)
	if err != nil {
		t.Fatalf("getChainInfo: %v", err)
	}
	if info.Project != "proj" || info.EntryFile != "index.html" {
		t.Fatalf("unexpected chain identity: project=%q entry=%q", info.Project, info.EntryFile)
	}

	// Sessions row carries chain_id / version_no.
	var storedChainID string
	var storedVersion int
	err = store.db.QueryRow(`SELECT COALESCE(chain_id,''), COALESCE(version_no,0) FROM sessions WHERE session_id = ?`, sid).Scan(&storedChainID, &storedVersion)
	if err != nil {
		t.Fatalf("read sessions row: %v", err)
	}
	if storedChainID != cid {
		t.Fatalf("sessions.chain_id mismatch: got %q want %q", storedChainID, cid)
	}
	if storedVersion != 1 {
		t.Fatalf("sessions.version_no mismatch: got %d want 1", storedVersion)
	}
}

func TestLinkToChain_AppendsToExistingChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, cid1, v1 := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a")
	_, cid2, v2 := createLinkedSession(t, store, "/elsewhere/index.html", "proj", "cat", "", "a", "b")
	_, cid3, v3 := createLinkedSession(t, store, "/deep/path/index.html", "proj", "cat", "", "a")

	if cid1 != cid2 || cid2 != cid3 {
		t.Fatalf("expected same chain id across sends; got %q %q %q", cid1, cid2, cid3)
	}
	if v1 != 1 || v2 != 2 || v3 != 3 {
		t.Fatalf("version numbers should be 1,2,3; got %d,%d,%d", v1, v2, v3)
	}
}

func TestLinkToChain_DifferentProjectDifferentChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, cidProjA, _ := createLinkedSession(t, store, "/work/index.html", "proj-a", "cat", "")
	_, cidProjB, _ := createLinkedSession(t, store, "/work/index.html", "proj-b", "cat", "")

	if cidProjA == cidProjB {
		t.Fatal("sessions under different projects must not share a chain")
	}
}

func TestLinkToChain_DifferentEntryFileDifferentChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, cidIndex, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	_, cidReport, _ := createLinkedSession(t, store, "/work/report.html", "proj", "cat", "")

	if cidIndex == cidReport {
		t.Fatal("sessions with different entry basenames must not share a chain")
	}
}

func TestLinkToChain_OwnerIsolation(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, cidAlice, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "alice")
	_, cidBob, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "bob")

	if cidAlice == cidBob {
		t.Fatal("same project+entry under different owners must not share a chain")
	}

	// A second send by alice rejoins alice's chain, not bob's.
	_, cidAlice2, vAlice2 := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "alice")
	if cidAlice2 != cidAlice {
		t.Fatalf("alice's second send should reuse her chain; got %q want %q", cidAlice2, cidAlice)
	}
	if vAlice2 != 2 {
		t.Fatalf("alice's second send should be v2; got v%d", vAlice2)
	}
}

func TestLinkToChain_AnonymousOwnersShareGlobalChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	_, cid1, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	_, cid2, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")

	if cid1 != cid2 {
		t.Fatal("with auth disabled (empty owner), same project+entry should share a global chain")
	}
}

func TestLinkToChain_MissingSession(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if _, _, err := store.LinkToChain("nonexistent", "proj", "index.html", ""); err == nil {
		t.Fatal("expected ErrSessionNotFound for unknown session id")
	}
}

func TestLinkToChain_EmptyProjectOrEntry(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.LinkToChain(sess.ID, "", "index.html", ""); err == nil {
		t.Fatal("expected error when project is empty")
	}
	if _, _, err := store.LinkToChain(sess.ID, "proj", "", ""); err == nil {
		t.Fatal("expected error when entry file basename is empty")
	}
}

func TestGetChainOfSession_OrdersByVersionNo(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	sid1, _, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a")
	// Tiny sleep so created_at distinguishes rows even though we order by version_no.
	sid2, _, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a", "b")
	sid3, cid, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a")

	info, versions, err := store.GetChainOfSession(sid3)
	if err != nil {
		t.Fatalf("GetChainOfSession: %v", err)
	}
	if info.ChainID != cid {
		t.Fatalf("chain id mismatch: got %q want %q", info.ChainID, cid)
	}
	if info.VersionNum != 3 {
		t.Fatalf("expected 3 versions in chain; got %d", info.VersionNum)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 version rows; got %d", len(versions))
	}

	// Ordered ascending by version_no.
	wantVersions := []int{1, 2, 3}
	for i, v := range versions {
		if v.VersionNo != wantVersions[i] {
			t.Fatalf("versions[%d].VersionNo = %d, want %d", i, v.VersionNo, wantVersions[i])
		}
	}

	// Current flag is set on the requested session only.
	if !versions[2].Current || versions[0].Current || versions[1].Current {
		t.Fatalf("expected only the last version to be current; got %+v", []bool{versions[0].Current, versions[1].Current, versions[2].Current})
	}
	if versions[2].SessionID != sid3 {
		t.Fatalf("current session id mismatch: got %q want %q", versions[2].SessionID, sid3)
	}

	// Chain identity surfaces the session ids in order too.
	if versions[0].SessionID != sid1 || versions[1].SessionID != sid2 {
		t.Fatalf("session ids out of order: got %q,%q,%q", versions[0].SessionID, versions[1].SessionID, versions[2].SessionID)
	}
}

func TestGetChainOfSession_SkipsSoftDeleted(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	sid1, cid, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")

	// Soft-delete v2 (the middle session). Use a direct UPDATE to know which row
	// we're hitting, since we only have session ids.
	_, versions, err := store.GetChainOfSession(sid1)
	if err != nil {
		t.Fatalf("GetChainOfSession before delete: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions pre-delete; got %d", len(versions))
	}
	middleID := versions[1].SessionID

	if err := store.SoftDelete(middleID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	_, after, err := store.GetChainOfSession(sid1)
	if err != nil {
		t.Fatalf("GetChainOfSession after delete: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("soft-deleted version should be skipped; got %d versions", len(after))
	}

	// The chain row itself survives.
	info, err := store.getChainInfo(cid)
	if err != nil {
		t.Fatalf("chain row should survive soft-delete of a member: %v", err)
	}
	if info.ChainID != cid {
		t.Fatal("chain identity lost")
	}
}

// TestGetChainOfSession_RequestedSessionSoftDeleted_ReturnsNotFound is the
// regression guard for the CodeRabbit finding: requesting the chain of a
// soft-deleted session must surface ErrSessionNotFound (rather than silently
// returning the rest of the chain and fabricating a "current" version).
func TestGetChainOfSession_RequestedSessionSoftDeleted_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	target, _, _ := createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")
	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "")

	if err := store.SoftDelete(target); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	_, _, err := store.GetChainOfSession(target)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound for soft-deleted session, got %v", err)
	}
}

func TestGetChainOfSession_NotLinkedReturnsSingleVersion(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	sess, err := store.CreateUploaded("/work/index.html", "/work/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(sess.ID, "proj"); err != nil {
		t.Fatal(err)
	}

	info, versions, err := store.GetChainOfSession(sess.ID)
	if err != nil {
		t.Fatalf("GetChainOfSession on unlinked session: %v", err)
	}
	if info.ChainID != "" {
		t.Fatalf("expected empty chain id for unlinked session; got %q", info.ChainID)
	}
	if info.VersionNum != 1 {
		t.Fatalf("expected synthesized 1-version view; got %d", info.VersionNum)
	}
	if len(versions) != 1 {
		t.Fatalf("expected exactly 1 synthesized version; got %d", len(versions))
	}
	if !versions[0].Current {
		t.Fatal("the sole version should be flagged current")
	}
	if versions[0].Project != "proj" {
		t.Fatalf("synthesized version should carry session metadata; project=%q", versions[0].Project)
	}
}

func TestDiffChainMetadata_TagsCategoryProjectTransitions(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	// v1: tags={a,b}, category=c1, project=p
	createLinkedSession(t, store, "/work/index.html", "p", "c1", "", "a", "b")
	// v2: drop b, add c; category c1->c2
	createLinkedSession(t, store, "/work/index.html", "p", "c2", "", "a", "c")
	// v3: drop c, add d,e; project stays p, category stays c2
	_, cid, _ := createLinkedSession(t, store, "/work/index.html", "p", "c2", "", "a", "d", "e")

	diffs, err := store.DiffChainMetadata(cid)
	if err != nil {
		t.Fatalf("DiffChainMetadata: %v", err)
	}
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs (one per version, baseline=v0); got %d", len(diffs))
	}

	// v1 vs baseline
	d0 := diffs[0]
	if d0.ToVersion != 1 || d0.FromVersion != 0 {
		t.Fatalf("diff[0] versions wrong: from=%d to=%d", d0.FromVersion, d0.ToVersion)
	}
	sort.Strings(d0.AddedTags)
	if !equalStringSlice(d0.AddedTags, []string{"a", "b"}) {
		t.Fatalf("v1 added tags = %v, want [a b]", d0.AddedTags)
	}
	if len(d0.RemovedTags) != 0 {
		t.Fatalf("v1 should remove nothing; got %v", d0.RemovedTags)
	}
	if d0.CategoryNew != "c1" || d0.ProjectNew != "p" {
		t.Fatalf("v1 baseline category/project wrong: %+v", d0)
	}

	// v2 vs v1: add c, remove b; category c1->c2
	d1 := diffs[1]
	if d1.ToVersion != 2 {
		t.Fatalf("diff[1] to=%d want 2", d1.ToVersion)
	}
	if !equalStringSlice(d1.AddedTags, []string{"c"}) || !equalStringSlice(d1.RemovedTags, []string{"b"}) {
		t.Fatalf("v2 diff wrong: added=%v removed=%v", d1.AddedTags, d1.RemovedTags)
	}
	if d1.CategoryOld != "c1" || d1.CategoryNew != "c2" {
		t.Fatalf("v2 category transition wrong: %q -> %q", d1.CategoryOld, d1.CategoryNew)
	}

	// v3 vs v2: add d,e; remove c; project/category unchanged
	d2 := diffs[2]
	sort.Strings(d2.AddedTags)
	if !equalStringSlice(d2.AddedTags, []string{"d", "e"}) || !equalStringSlice(d2.RemovedTags, []string{"c"}) {
		t.Fatalf("v3 diff wrong: added=%v removed=%v", d2.AddedTags, d2.RemovedTags)
	}
	if d2.CategoryOld != "c2" || d2.CategoryNew != "c2" {
		t.Fatalf("v3 category should be unchanged: %q -> %q", d2.CategoryOld, d2.CategoryNew)
	}
}

func TestDiffChainMetadata_EmptyChain(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	// A chain row with no live sessions (hypothetical edge). Use a random id.
	diffs, err := store.DiffChainMetadata("never-existed")
	if err != nil {
		t.Fatalf("DiffChainMetadata on unknown chain: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected 0 diffs for empty chain; got %d", len(diffs))
	}
}

func TestListDocuments_IncludesChainFields(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)

	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a")
	createLinkedSession(t, store, "/work/index.html", "proj", "cat", "", "a")

	docs, err := store.ListDocuments(FilterOptions{})
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs; got %d", len(docs))
	}

	// Both docs share a chain id; version numbers are distinct.
	if docs[0].ChainID == "" || docs[1].ChainID == "" {
		t.Fatal("expected non-empty chain id in document list")
	}
	if docs[0].ChainID != docs[1].ChainID {
		t.Fatalf("expected same chain id; got %q vs %q", docs[0].ChainID, docs[1].ChainID)
	}
	versionSet := map[int]bool{docs[0].VersionNo: true, docs[1].VersionNo: true}
	if !versionSet[1] || !versionSet[2] {
		t.Fatalf("expected version numbers {1,2}; got %d,%d", docs[0].VersionNo, docs[1].VersionNo)
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sortedA := append([]string{}, a...)
	sortedB := append([]string{}, b...)
	sort.Strings(sortedA)
	sort.Strings(sortedB)
	for i := range sortedA {
		if sortedA[i] != sortedB[i] {
			return false
		}
	}
	return true
}

// guard against unused import if filepath becomes unused in future edits.
var _ = filepath.Base
