package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"HumpYard/internal/jsonx"
)

// GenesisHash is the previous-hash value of the first audit record.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditRecord is one link of the hash chain. Hash covers the record fields and
// the previous hash, so any edit to an earlier record invalidates the chain.
type AuditRecord struct {
	Seq      int    `json:"seq"`
	Action   string `json:"action"`
	Subject  string `json:"subject"`
	Detail   string `json:"detail"`
	Payload  string `json:"payload_sha256"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

// ChainInput is the canonical byte string a record hashes over. The separator
// cannot appear in the hex digests and is escaped in free text.
func ChainInput(seq int, action, subject, detail, payload, prevHash string) string {
	fields := []string{
		strconv.Itoa(seq),
		escape(action),
		escape(subject),
		escape(detail),
		payload,
		prevHash,
	}
	return strings.Join(fields, "\x1f")
}

// escape removes the field separator and newlines from free text.
func escape(s string) string {
	s = strings.ReplaceAll(s, "\x1f", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// computeHash returns the hex digest of a chain input.
func computeHash(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// Append adds one audit record, linking it to the current chain head.
func (s *Store) Append(action, subject, detail string, payload any) (AuditRecord, error) {
	payloadHash := ""
	if payload != nil {
		digest, err := Digest(payload)
		if err != nil {
			return AuditRecord{}, err
		}
		payloadHash = digest
	} else {
		payloadHash = strings.Repeat("0", 64)
	}
	prev := s.meta.LastAuditHash
	if prev == "" {
		prev = GenesisHash
	}
	rec := AuditRecord{
		Seq:      s.meta.AuditRecords + 1,
		Action:   escape(action),
		Subject:  escape(subject),
		Detail:   escape(detail),
		Payload:  payloadHash,
		PrevHash: prev,
	}
	rec.Hash = computeHash(ChainInput(rec.Seq, rec.Action, rec.Subject, rec.Detail, rec.Payload, rec.PrevHash))
	line, err := jsonx.MarshalCanonical(rec)
	if err != nil {
		return AuditRecord{}, err
	}
	f, err := os.OpenFile(s.path(AuditFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("open audit log: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return AuditRecord{}, fmt.Errorf("write audit log: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return AuditRecord{}, fmt.Errorf("sync audit log: %w", err)
	}
	if err := f.Close(); err != nil {
		return AuditRecord{}, fmt.Errorf("close audit log: %w", err)
	}
	s.meta.AuditRecords = rec.Seq
	s.meta.LastAuditHash = rec.Hash
	if err := s.writeMeta(); err != nil {
		return AuditRecord{}, err
	}
	return rec, nil
}

// ReadAudit reads every audit record in file order.
func (s *Store) ReadAudit() ([]AuditRecord, error) {
	data, err := os.ReadFile(s.path(AuditFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	records, err := jsonx.SplitJSONL(data)
	if err != nil {
		return nil, err
	}
	out := make([]AuditRecord, 0, len(records))
	for _, rec := range records {
		var ar AuditRecord
		if err := jsonx.DecodeRecord(rec, &ar); err != nil {
			return nil, fmt.Errorf("audit log: %w", err)
		}
		out = append(out, ar)
	}
	return out, nil
}

// ChainProblem is one defect found while verifying the audit chain.
type ChainProblem struct {
	Seq      int    `json:"seq"`
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Message  string `json:"message"`
}

// Defect kinds reported by VerifyChain.
const (
	DefectSequence = "sequence"
	DefectLink     = "broken-link"
	DefectHash     = "hash-mismatch"
	DefectHead     = "head-mismatch"
	DefectCount    = "count-mismatch"
)

// ChainReport is the outcome of verifying a store.
type ChainReport struct {
	Records   int            `json:"records"`
	Head      string         `json:"head_hash"`
	MetaHead  string         `json:"meta_head_hash"`
	Snapshot  string         `json:"snapshot_sha256"`
	MetaSnap  string         `json:"meta_snapshot_sha256"`
	LedgerOK  bool           `json:"ledger_ok"`
	ChainOK   bool           `json:"chain_ok"`
	StoreOK   bool           `json:"store_ok"`
	Problems  []ChainProblem `json:"problems"`
	LedgerErr string         `json:"ledger_error"`
}

// VerifyChain recomputes the audit chain and cross-checks metadata, the ledger
// sequence and the snapshot digest.
func (s *Store) VerifyChain() (ChainReport, error) {
	records, err := s.ReadAudit()
	if err != nil {
		return ChainReport{}, err
	}
	rep := ChainReport{
		Records:  len(records),
		MetaHead: s.meta.LastAuditHash,
		MetaSnap: s.meta.SnapshotSHA,
		Head:     GenesisHash,
	}
	prev := GenesisHash
	for i, rec := range records {
		if rec.Seq != i+1 {
			rep.Problems = append(rep.Problems, ChainProblem{
				Seq: rec.Seq, Kind: DefectSequence,
				Expected: strconv.Itoa(i + 1), Actual: strconv.Itoa(rec.Seq),
				Message: "audit record is out of sequence",
			})
		}
		if rec.PrevHash != prev {
			rep.Problems = append(rep.Problems, ChainProblem{
				Seq: rec.Seq, Kind: DefectLink, Expected: prev, Actual: rec.PrevHash,
				Message: "record does not link to the previous hash",
			})
		}
		want := computeHash(ChainInput(rec.Seq, rec.Action, rec.Subject, rec.Detail, rec.Payload, rec.PrevHash))
		if want != rec.Hash {
			rep.Problems = append(rep.Problems, ChainProblem{
				Seq: rec.Seq, Kind: DefectHash, Expected: want, Actual: rec.Hash,
				Message: "record hash does not match its content",
			})
		}
		// Carry the recomputed hash forward, not the stored one, so a single
		// edited record also breaks every link that follows it.
		prev = want
	}
	rep.Head = prev
	if s.meta.AuditRecords != len(records) {
		rep.Problems = append(rep.Problems, ChainProblem{
			Seq: len(records), Kind: DefectCount,
			Expected: strconv.Itoa(s.meta.AuditRecords), Actual: strconv.Itoa(len(records)),
			Message: "metadata record count does not match the audit log",
		})
	}
	if len(records) > 0 && s.meta.LastAuditHash != prev {
		rep.Problems = append(rep.Problems, ChainProblem{
			Seq: len(records), Kind: DefectHead,
			Expected: prev, Actual: s.meta.LastAuditHash,
			Message: "metadata head hash does not match the audit log",
		})
	}
	rep.ChainOK = len(rep.Problems) == 0
	if entries, err := s.ReadLedger(); err != nil {
		rep.LedgerErr = err.Error()
	} else if len(entries) != s.meta.LedgerEntries {
		rep.LedgerErr = fmt.Sprintf("metadata reports %d ledger entries, file holds %d", s.meta.LedgerEntries, len(entries))
	} else {
		rep.LedgerOK = true
	}
	if s.meta.SnapshotSHA != "" {
		digest, err := s.SnapshotDigest()
		if err != nil {
			rep.Snapshot = ""
		} else {
			rep.Snapshot = digest
		}
	}
	snapshotOK := s.meta.SnapshotSHA == "" || s.meta.SnapshotSHA == rep.Snapshot
	if !snapshotOK {
		rep.Problems = append(rep.Problems, ChainProblem{
			Kind: DefectHash, Expected: s.meta.SnapshotSHA, Actual: rep.Snapshot,
			Message: "plan snapshot digest does not match metadata",
		})
		rep.ChainOK = false
	}
	rep.StoreOK = rep.ChainOK && rep.LedgerOK
	return rep, nil
}
