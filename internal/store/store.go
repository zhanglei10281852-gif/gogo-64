// Package store is the local persistence layer. A store is a directory holding
// an append-only work ledger, the latest plan snapshot, store metadata and a
// hash-chained audit log. Every whole-file write is atomic: content goes to a
// temporary file in the same directory, is flushed, and is then renamed over
// the target.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"HumpYard/internal/jsonx"
)

// File names inside a store directory.
const (
	MetaFile     = "meta.json"
	LedgerFile   = "ledger.jsonl"
	SnapshotFile = "plan.json"
	AuditFile    = "audit.jsonl"
)

// FormatVersion is the on-disk layout version.
const FormatVersion = 1

// Meta is the store metadata document.
type Meta struct {
	FormatVersion int    `json:"format_version"`
	YardID        string `json:"yard_id"`
	OrderID       string `json:"order_id"`
	LedgerEntries int    `json:"ledger_entries"`
	AuditRecords  int    `json:"audit_records"`
	Snapshots     int    `json:"snapshots"`
	LastAuditHash string `json:"last_audit_hash"`
	SnapshotSHA   string `json:"snapshot_sha256"`
}

// LedgerEntry is one immutable work record.
type LedgerEntry struct {
	Seq     int    `json:"seq"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	TrackID string `json:"track_id"`
	Cars    int    `json:"cars"`
	Minutes int    `json:"minutes"`
	Detail  string `json:"detail"`
}

// Store is an opened store directory.
type Store struct {
	dir  string
	meta Meta
}

// Open opens a store, creating the directory and metadata when absent.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store %s: %w", dir, err)
	}
	s := &Store{dir: dir}
	meta, err := s.readMeta()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.meta = Meta{FormatVersion: FormatVersion, LastAuditHash: GenesisHash}
		if err := s.writeMeta(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if meta.FormatVersion != FormatVersion {
			return nil, fmt.Errorf("store %s has format version %d, expected %d", dir, meta.FormatVersion, FormatVersion)
		}
		s.meta = meta
	}
	return s, nil
}

// Dir returns the store directory.
func (s *Store) Dir() string {
	return s.dir
}

// Meta returns a copy of the current metadata.
func (s *Store) Meta() Meta {
	return s.meta
}

// SetIdentity records the yard and order the store describes.
func (s *Store) SetIdentity(yardID, orderID string) error {
	s.meta.YardID = yardID
	s.meta.OrderID = orderID
	return s.writeMeta()
}

// path resolves a file inside the store.
func (s *Store) path(name string) string {
	return filepath.Join(s.dir, name)
}

// readMeta loads the metadata document.
func (s *Store) readMeta() (Meta, error) {
	var meta Meta
	data, err := os.ReadFile(s.path(MetaFile))
	if err != nil {
		return meta, err
	}
	if err := jsonx.DecodeStrict(data, &meta); err != nil {
		return meta, fmt.Errorf("decoding %s: %w", MetaFile, err)
	}
	return meta, nil
}

// writeMeta persists the metadata document atomically.
func (s *Store) writeMeta() error {
	data, err := jsonx.MarshalIndent(s.meta)
	if err != nil {
		return err
	}
	return WriteAtomic(s.path(MetaFile), data)
}

// AppendLedger appends work entries to the ledger, numbering them from the
// current entry count. The ledger is never rewritten.
func (s *Store) AppendLedger(entries []LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	f, err := os.OpenFile(s.path(LedgerFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer f.Close()
	seq := s.meta.LedgerEntries
	for _, e := range entries {
		seq++
		e.Seq = seq
		line, err := jsonx.MarshalCanonical(e)
		if err != nil {
			return err
		}
		if _, err := f.Write(line); err != nil {
			return fmt.Errorf("write ledger: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync ledger: %w", err)
	}
	s.meta.LedgerEntries = seq
	return s.writeMeta()
}

// ReadLedger reads every ledger entry in file order and checks the sequence.
func (s *Store) ReadLedger() ([]LedgerEntry, error) {
	data, err := os.ReadFile(s.path(LedgerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	records, err := jsonx.SplitJSONL(data)
	if err != nil {
		return nil, err
	}
	out := make([]LedgerEntry, 0, len(records))
	for i, rec := range records {
		var e LedgerEntry
		if err := jsonx.DecodeRecord(rec, &e); err != nil {
			return nil, fmt.Errorf("ledger: %w", err)
		}
		if e.Seq != i+1 {
			return nil, fmt.Errorf("ledger line %d: seq %d breaks the sequence", rec.Line, e.Seq)
		}
		out = append(out, e)
	}
	return out, nil
}

// SaveSnapshot writes the plan snapshot atomically and records its digest.
func (s *Store) SaveSnapshot(snapshot any) error {
	data, err := jsonx.MarshalIndent(snapshot)
	if err != nil {
		return err
	}
	if err := WriteAtomic(s.path(SnapshotFile), data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	s.meta.SnapshotSHA = hex.EncodeToString(sum[:])
	s.meta.Snapshots++
	return s.writeMeta()
}

// LoadSnapshot reads the plan snapshot into out.
func (s *Store) LoadSnapshot(out any) error {
	data, err := os.ReadFile(s.path(SnapshotFile))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no plan snapshot in %s; run the plan command first", s.dir)
	}
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	if err := jsonx.DecodeStrict(data, out); err != nil {
		return fmt.Errorf("decoding %s: %w", SnapshotFile, err)
	}
	return nil
}

// SnapshotDigest returns the hex SHA-256 of the stored snapshot bytes.
func (s *Store) SnapshotDigest() (string, error) {
	data, err := os.ReadFile(s.path(SnapshotFile))
	if err != nil {
		return "", fmt.Errorf("read snapshot: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Files lists the store files present on disk in lexical order.
func (s *Store) Files() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read store dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// WriteAtomic writes data to path via a temporary file and a rename.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// Digest returns the hex SHA-256 of the canonical JSON encoding of v.
func Digest(v any) (string, error) {
	data, err := jsonx.MarshalCanonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
