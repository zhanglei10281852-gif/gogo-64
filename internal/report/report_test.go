package report

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"HumpYard/internal/config"
	"HumpYard/internal/ingest"
	"HumpYard/internal/pipeline"
	"HumpYard/internal/store"
)

// snapshot runs the pipeline over the example data.
func snapshot(t *testing.T) (*config.Config, *ingest.Result, *pipeline.Snapshot) {
	t.Helper()
	cfg, _, err := config.Load(filepath.Join("..", "..", "examples", "config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(filepath.Join("..", "..", "examples", "order.json"), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	snap, err := pipeline.Run(cfg, res)
	if err != nil {
		t.Fatalf("pipeline.Run: %v", err)
	}
	return cfg, res, snap
}

func TestMinutesFormatting(t *testing.T) {
	cases := map[int]string{0: "0+00:00", 90: "0+01:30", 1439: "0+23:59", 1440: "1+00:00", 1500: "1+01:00"}
	for minute, want := range cases {
		if got := Minutes(minute); got != want {
			t.Fatalf("Minutes(%d) = %q, want %q", minute, got, want)
		}
	}
}

func TestSnapshotTextIsDeterministic(t *testing.T) {
	_, _, snap := snapshot(t)
	first := Snapshot(snap)
	for i := 0; i < 5; i++ {
		if again := Snapshot(snap); again != first {
			t.Fatal("snapshot text rendering is not deterministic")
		}
	}
	for _, want := range []string{"HUMPYARD PLAN", "BLOCKING SUMMARY", "HUMP SEQUENCE",
		"TRACK OCCUPANCY", "HAZMAT VALIDATION", "DEPARTURE PROGRAM", "REHANDLE", "SHIFT PLAN"} {
		if !strings.Contains(first, want) {
			t.Fatalf("rendering is missing the %q section", want)
		}
	}
}

func TestSnapshotTextCarriesNoWallClock(t *testing.T) {
	_, _, snap := snapshot(t)
	text := Snapshot(snap)
	patterns := []string{
		`\d{4}-\d{2}-\d{2}`,       // calendar date
		`\d{2}:\d{2}:\d{2}`,       // wall clock time
		`(?i)\b(utc|gmt|z)\b\s*$`, // time zone marker at end of line
	}
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if loc := re.FindString(text); loc != "" {
			t.Fatalf("rendering contains %q, which looks like a wall clock reading", loc)
		}
	}
}

func TestStageRenderings(t *testing.T) {
	cfg, res, snap := snapshot(t)
	if got := Validation(cfg, config.Report{Findings: res.Findings}); !strings.Contains(got, "CONFIGURATION") {
		t.Fatalf("validation rendering %q", got)
	}
	if got := Ingested(res); !strings.Contains(got, "YARD ORDER WBKH-2201") {
		t.Fatalf("ingest rendering %q", got)
	}
	if got := Blocking(snap.Blocking); !strings.Contains(got, "TRACK LOADING") {
		t.Fatalf("blocking rendering %q", got)
	}
	if got := Hump(snap.Hump); !strings.Contains(got, "FLAT SWITCHING") {
		t.Fatalf("hump rendering %q", got)
	}
	if got := Occupancy(snap.Occupancy); !strings.Contains(got, "FINAL STANDING ORDER") {
		t.Fatalf("occupancy rendering %q", got)
	}
	if got := Hazmat(snap.Hazmat); !strings.Contains(got, "EXPOSURE") {
		t.Fatalf("hazmat rendering %q", got)
	}
	if got := Departures(snap.Departures); !strings.Contains(got, "TRAIN T410") {
		t.Fatalf("departure rendering %q", got)
	}
	if got := Rehandle(snap.Rehandle); !strings.Contains(got, "CATEGORIES") {
		t.Fatalf("rehandle rendering %q", got)
	}
	if got := Shifts(snap.Shifts); !strings.Contains(got, "CREW LOAD") {
		t.Fatalf("shift rendering %q", got)
	}
}

func TestFindingsRenderingHandlesEmptyList(t *testing.T) {
	if got := Findings(nil); !strings.Contains(got, "no findings") {
		t.Fatalf("empty findings rendered as %q", got)
	}
	got := Findings([]config.Finding{{Severity: config.SeverityError, Scope: "hazmat", Subject: "C01", Message: "too close"}})
	if !strings.Contains(got, "error") || !strings.Contains(got, "too close") {
		t.Fatalf("finding rendered as %q", got)
	}
}

func TestJSONWritesIndentedDocument(t *testing.T) {
	_, _, snap := snapshot(t)
	var buf bytes.Buffer
	if err := JSON(&buf, snap.Digest()); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(buf.String(), "\n  \"inbound_cars\"") {
		t.Fatalf("output is not indented: %q", buf.String())
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestLedgerAndAuditRendering(t *testing.T) {
	if got := Ledger(nil); !strings.Contains(got, "empty") {
		t.Fatalf("empty ledger rendered as %q", got)
	}
	entries := []store.LedgerEntry{{Seq: 1, Kind: "movement-hump", Subject: "T101", TrackID: "C01", Cars: 3, Minutes: 2, Detail: "retarder heavy"}}
	if got := Ledger(entries); !strings.Contains(got, "movement-hump") {
		t.Fatalf("ledger rendered as %q", got)
	}
	if got := Audit(nil); !strings.Contains(got, "empty") {
		t.Fatalf("empty audit rendered as %q", got)
	}
	records := []store.AuditRecord{{Seq: 1, Action: "plan", Subject: "WBKH", Detail: "ok", Hash: strings.Repeat("a", 64)}}
	if got := Audit(records); !strings.Contains(got, "aaaaaaaaaaaaaaaa") {
		t.Fatalf("audit rendered as %q", got)
	}
}

func TestVerifyRendering(t *testing.T) {
	rep := store.ChainReport{Records: 2, Head: strings.Repeat("b", 64), MetaHead: strings.Repeat("b", 64),
		LedgerOK: true, ChainOK: true, StoreOK: true}
	meta := store.Meta{FormatVersion: 1, YardID: "WBKH", OrderID: "WBKH-2201", LedgerEntries: 5, AuditRecords: 2}
	got := Verify(rep, meta, []string{"audit.jsonl", "meta.json"})
	if !strings.Contains(got, "STORE VERIFICATION") || !strings.Contains(got, "none") {
		t.Fatalf("verify rendering %q", got)
	}
	rep.StoreOK = false
	rep.Problems = []store.ChainProblem{{Seq: 1, Kind: store.DefectHash, Expected: "x", Actual: "y", Message: "bad"}}
	got = Verify(rep, meta, nil)
	if !strings.Contains(got, store.DefectHash) || !strings.Contains(got, "bad") {
		t.Fatalf("problem rendering %q", got)
	}
}

func TestTruncateAndHelpers(t *testing.T) {
	if got := truncate("abcdefgh", 5); got != "ab..." {
		t.Fatalf("truncate gave %q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("truncate gave %q", got)
	}
	if got := truncate("abcdef", 2); got != "ab" {
		t.Fatalf("truncate gave %q", got)
	}
	if dash("") != "-" || dash("C01") != "C01" {
		t.Fatal("dash is wrong")
	}
	if yesNo(true) != "yes" || yesNo(false) != "no" {
		t.Fatal("yesNo is wrong")
	}
}
