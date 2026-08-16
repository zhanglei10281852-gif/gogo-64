package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotFixture is a small stand-in for a plan snapshot.
type snapshotFixture struct {
	Yard  string `json:"yard"`
	Cars  int    `json:"cars"`
	Notes string `json:"notes"`
}

func TestOpenCreatesMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if st.Meta().FormatVersion != FormatVersion {
		t.Fatalf("format version %d", st.Meta().FormatVersion)
	}
	if st.Meta().LastAuditHash != GenesisHash {
		t.Fatalf("genesis hash %q", st.Meta().LastAuditHash)
	}
	if _, err := os.Stat(filepath.Join(dir, MetaFile)); err != nil {
		t.Fatalf("metadata was not written: %v", err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if again.Meta() != st.Meta() {
		t.Fatalf("metadata changed on reopen: %+v then %+v", st.Meta(), again.Meta())
	}
}

func TestOpenRejectsForeignFormatVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MetaFile), []byte(`{"format_version":99}`), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected a format version error")
	}
}

func TestAppendLedgerNumbersEntries(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	first := []LedgerEntry{
		{Kind: "movement-hump", Subject: "T101", TrackID: "C01", Cars: 3, Minutes: 2},
		{Kind: "movement-flat", Subject: "T101", TrackID: "C09", Cars: 1, Minutes: 6},
	}
	if err := st.AppendLedger(first); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	if err := st.AppendLedger([]LedgerEntry{{Kind: "departure", Subject: "T410", Cars: 20}}); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	entries, err := st.ReadLedger()
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.Seq != i+1 {
			t.Fatalf("entry %d has seq %d", i, e.Seq)
		}
	}
	if st.Meta().LedgerEntries != 3 {
		t.Fatalf("metadata counts %d entries", st.Meta().LedgerEntries)
	}
	if entries[2].Subject != "T410" {
		t.Fatalf("unexpected third entry %+v", entries[2])
	}
}

func TestAppendLedgerIgnoresEmptyBatch(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.AppendLedger(nil); err != nil {
		t.Fatalf("AppendLedger: %v", err)
	}
	if entries, err := st.ReadLedger(); err != nil || len(entries) != 0 {
		t.Fatalf("ledger should be empty, got %v %v", entries, err)
	}
}

func TestSnapshotRoundTripAndDigest(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := snapshotFixture{Yard: "WBKH", Cars: 44, Notes: "first pass"}
	if err := st.SaveSnapshot(want); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	var got snapshotFixture
	if err := st.LoadSnapshot(&got); err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	if got != want {
		t.Fatalf("round trip gave %+v", got)
	}
	digest, err := st.SnapshotDigest()
	if err != nil {
		t.Fatalf("SnapshotDigest: %v", err)
	}
	if digest != st.Meta().SnapshotSHA {
		t.Fatalf("digest %s does not match metadata %s", digest, st.Meta().SnapshotSHA)
	}
	if err := st.SaveSnapshot(want); err != nil {
		t.Fatalf("second SaveSnapshot: %v", err)
	}
	if again, _ := st.SnapshotDigest(); again != digest {
		t.Fatal("the same snapshot must produce the same digest")
	}
	if st.Meta().Snapshots != 2 {
		t.Fatalf("snapshot counter %d", st.Meta().Snapshots)
	}
}

func TestLoadSnapshotWithoutFile(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var got snapshotFixture
	err = st.LoadSnapshot(&got)
	if err == nil || !strings.Contains(err.Error(), "no plan snapshot") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAuditChainVerifies(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.SetIdentity("WBKH", "WBKH-2201"); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	prev := GenesisHash
	for i := 0; i < 4; i++ {
		rec, err := st.Append("plan", "WBKH", "step", snapshotFixture{Cars: i})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if rec.PrevHash != prev {
			t.Fatalf("record %d links to %s, want %s", rec.Seq, rec.PrevHash, prev)
		}
		prev = rec.Hash
	}
	rep, err := st.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !rep.StoreOK || !rep.ChainOK || !rep.LedgerOK {
		t.Fatalf("store should verify: %+v", rep)
	}
	if rep.Records != 4 || rep.Head != prev {
		t.Fatalf("unexpected report %+v", rep)
	}
	if files, err := st.Files(); err != nil || len(files) < 2 {
		t.Fatalf("files %v %v", files, err)
	}
}

func TestAuditChainDetectsTamperedRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.Append("plan", "WBKH", "step", snapshotFixture{Cars: i}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	path := filepath.Join(dir, AuditFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	tampered := strings.Replace(string(data), `"detail":"step"`, `"detail":"tampered"`, 1)
	if tampered == string(data) {
		t.Fatal("test could not tamper with the audit log")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rep, err := reopened.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if rep.ChainOK || rep.StoreOK {
		t.Fatal("tampering must be detected")
	}
	kinds := map[string]bool{}
	for _, p := range rep.Problems {
		kinds[p.Kind] = true
	}
	if !kinds[DefectHash] {
		t.Fatalf("expected a hash mismatch, got %+v", rep.Problems)
	}
	if !kinds[DefectLink] {
		t.Fatalf("expected a broken link, got %+v", rep.Problems)
	}
}

func TestAuditChainDetectsTruncatedLog(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.Append("plan", "WBKH", "step", nil); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	path := filepath.Join(dir, AuditFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[:2], "")), 0o644); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rep, err := reopened.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if rep.ChainOK {
		t.Fatal("a truncated log must not verify")
	}
	found := false
	for _, p := range rep.Problems {
		if p.Kind == DefectCount || p.Kind == DefectHead {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a count or head mismatch, got %+v", rep.Problems)
	}
}

func TestVerifyDetectsSnapshotEdit(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.SaveSnapshot(snapshotFixture{Yard: "WBKH", Cars: 44}); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	path := filepath.Join(dir, SnapshotFile)
	if err := os.WriteFile(path, []byte(`{"yard":"WBKH","cars":45,"notes":""}`), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	rep, err := st.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if rep.StoreOK {
		t.Fatal("an edited snapshot must not verify")
	}
}

func TestWriteAtomicReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	if err := WriteAtomic(path, []byte("first")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := WriteAtomic(path, []byte("second")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("content %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary files were left behind: %d entries", len(entries))
	}
}

func TestChainInputEscapesSeparators(t *testing.T) {
	got := ChainInput(1, "plan\x1f", "yard\nid", " detail ", strings.Repeat("0", 64), GenesisHash)
	if strings.Count(got, "\x1f") != 5 {
		t.Fatalf("unexpected separator count in %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("newlines must be escaped: %q", got)
	}
}

func TestDigestIsStable(t *testing.T) {
	value := snapshotFixture{Yard: "WBKH", Cars: 44}
	first, err := Digest(value)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := Digest(value)
		if err != nil {
			t.Fatalf("Digest: %v", err)
		}
		if again != first {
			t.Fatal("digest is not stable")
		}
	}
	if len(first) != 64 {
		t.Fatalf("digest length %d", len(first))
	}
}

func TestOpenRequiresDirectory(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("expected an error for an empty directory")
	}
}
