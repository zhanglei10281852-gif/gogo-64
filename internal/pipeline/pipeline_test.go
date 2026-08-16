package pipeline

import (
	"path/filepath"
	"testing"

	"HumpYard/internal/config"
	"HumpYard/internal/ingest"
	"HumpYard/internal/jsonx"
	"HumpYard/internal/store"
)

// examplePath resolves a file in the repository examples directory.
func examplePath(name string) string {
	return filepath.Join("..", "..", "examples", name)
}

// load reads the example configuration and yard order.
func load(t *testing.T, orderName string) (*config.Config, *ingest.Result) {
	t.Helper()
	cfg, _, err := config.Load(examplePath("config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	res, err := ingest.Load(examplePath(orderName), cfg)
	if err != nil {
		t.Fatalf("ingest.Load: %v", err)
	}
	return cfg, res
}

func TestRunProducesACompleteSnapshot(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if snap.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version %d", snap.SchemaVersion)
	}
	if snap.YardID != cfg.Yard.ID || snap.OrderID != res.Order.OrderID {
		t.Fatalf("identity %s / %s", snap.YardID, snap.OrderID)
	}
	if len(snap.ConfigSHA) != 64 || len(snap.OrderSHA) != 64 {
		t.Fatalf("digests %q %q", snap.ConfigSHA, snap.OrderSHA)
	}
	if len(snap.Blocking.Assignments) != res.Order.CarCount() {
		t.Fatalf("blocking covers %d of %d cars", len(snap.Blocking.Assignments), res.Order.CarCount())
	}
	if snap.Hump.Stats.CarsHumped+snap.Hump.Stats.CarsFlat != res.Order.CarCount() {
		t.Fatal("crest plan does not cover every car")
	}
	if len(snap.Departures.Trains) != len(cfg.Departures) {
		t.Fatalf("built %d trains", len(snap.Departures.Trains))
	}
	if snap.Rehandle.TotalCars != res.Order.CarCount() {
		t.Fatalf("rework counted %d cars", snap.Rehandle.TotalCars)
	}
	if snap.Shifts.Stats.Tasks == 0 {
		t.Fatal("no shift tasks were derived")
	}
	if len(snap.Hazmat.Tallies) == 0 {
		t.Fatal("no hazmat tallies were produced")
	}
}

func TestRunIsDeterministic(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	first, err := jsonx.MarshalCanonical(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 5; i++ {
		cfg2, res2 := load(t, "order.json")
		again, err := Run(cfg2, res2)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		data, err := jsonx.MarshalCanonical(again)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(data) != string(first) {
			t.Fatal("the pipeline is not deterministic across runs")
		}
	}
}

func TestFindingsAreMergedAndDeduplicated(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range snap.Findings {
		key := f.Severity + "|" + f.Scope + "|" + f.Subject + "|" + f.Message
		if seen[key] {
			t.Fatalf("duplicate finding %q", key)
		}
		seen[key] = true
	}
	for i := 1; i < len(snap.Findings); i++ {
		if snap.Findings[i-1].Severity > snap.Findings[i].Severity {
			t.Fatal("findings are not sorted by severity")
		}
	}
	counts := snap.Digest()
	if counts.Errors+counts.Warnings != len(snap.Findings) {
		t.Fatalf("digest counts %d findings, snapshot holds %d", counts.Errors+counts.Warnings, len(snap.Findings))
	}
}

func TestInboundPlacementsAreValidated(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	kinds := map[string]bool{}
	for _, tally := range snap.Hazmat.Tallies {
		kinds[tally.TrackID] = true
	}
	for _, train := range res.Order.Trains {
		if !kinds[train.ID] {
			t.Fatalf("arrival %s was not hazmat checked", train.ID)
		}
	}
}

func TestLedgerCoversEveryStage(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries := snap.Ledger()
	kinds := map[string]int{}
	for _, e := range entries {
		kinds[e.Kind]++
	}
	if kinds["movement-hump"] == 0 || kinds["movement-flat"] == 0 {
		t.Fatalf("movement entries missing: %v", kinds)
	}
	if kinds["departure"] != len(snap.Departures.Trains) {
		t.Fatalf("departure entries %d", kinds["departure"])
	}
	crew := 0
	for kind, n := range kinds {
		if len(kind) > 5 && kind[:5] == "crew-" {
			crew += n
		}
	}
	if crew != len(snap.Shifts.Assignments) {
		t.Fatalf("crew entries %d for %d assignments", crew, len(snap.Shifts.Assignments))
	}
}

func TestPersistAndReloadSnapshot(t *testing.T) {
	cfg, res := load(t, "order.json")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	records, err := Persist(st, snap)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if len(records) != 8 {
		t.Fatalf("expected 8 audit records, got %d", len(records))
	}
	rep, err := st.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !rep.StoreOK {
		t.Fatalf("store should verify: %+v", rep)
	}
	var reloaded Snapshot
	if err := st.LoadSnapshot(&reloaded); err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}
	want, err := jsonx.MarshalCanonical(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := jsonx.MarshalCanonical(&reloaded)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("snapshot did not survive the round trip")
	}
	entries, err := st.ReadLedger()
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(entries) != len(snap.Ledger()) {
		t.Fatalf("ledger holds %d entries, snapshot produced %d", len(entries), len(snap.Ledger()))
	}
	if st.Meta().OrderID != snap.OrderID {
		t.Fatalf("store identity %q", st.Meta().OrderID)
	}
}

func TestRunOnJSONLOrder(t *testing.T) {
	cfg, res := load(t, "order.jsonl")
	snap, err := Run(cfg, res)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if snap.OrderID != "WBKH-2202" {
		t.Fatalf("order id %q", snap.OrderID)
	}
	if snap.Ingest.Cars != 12 {
		t.Fatalf("ingested %d cars", snap.Ingest.Cars)
	}
	if snap.Departures.Stats.CarsForwarded == 0 {
		t.Fatal("no cars were forwarded")
	}
}
