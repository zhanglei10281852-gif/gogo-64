package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"HumpYard/internal/config"
)

// examplePath resolves a file in the repository examples directory.
func examplePath(name string) string {
	return filepath.Join("..", "..", "examples", name)
}

// loadConfig reads the example configuration.
func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(examplePath("config.json"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestLoadWholeDocumentOrder(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Order.OrderID != "WBKH-2201" {
		t.Fatalf("order id %q", res.Order.OrderID)
	}
	if res.Stats.Trains != 4 || res.Stats.Cars != 44 {
		t.Fatalf("stats %+v", res.Stats)
	}
	if res.Stats.BadOrderCars != 1 || res.Stats.DrawbarPairs != 1 {
		t.Fatalf("stats %+v", res.Stats)
	}
	if len(res.Stats.UnknownDestinations) != 1 || res.Stats.UnknownDestinations[0] != "XYZ" {
		t.Fatalf("unknown destinations %v", res.Stats.UnknownDestinations)
	}
	if HasErrors(res.Findings) {
		t.Fatalf("example order should not produce errors: %+v", res.Findings)
	}
}

func TestLoadJSONLOrder(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.jsonl"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Order.OrderID != "WBKH-2202" {
		t.Fatalf("order id %q", res.Order.OrderID)
	}
	if res.Stats.Trains != 2 || res.Stats.Cars != 12 {
		t.Fatalf("stats %+v", res.Stats)
	}
	if res.Order.Trains[0].ID != "T201" {
		t.Fatalf("first train %q", res.Order.Trains[0].ID)
	}
}

func TestTrainsAreSortedByArrival(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 1; i < len(res.Order.Trains); i++ {
		if res.Order.Trains[i-1].ArrivalMinute > res.Order.Trains[i].ArrivalMinute {
			t.Fatal("trains are not sorted by arrival minute")
		}
	}
}

func TestParseJSONRejectsUnknownCarField(t *testing.T) {
	data, err := os.ReadFile(examplePath("order.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	broken := strings.Replace(string(data), `"gross_tons"`, `"grosstons"`, 1)
	if _, err := parseJSON([]byte(broken)); err == nil {
		t.Fatal("expected a decode error for an unknown car field")
	}
}

func TestParseJSONLRequiresRecordTag(t *testing.T) {
	_, err := parseJSONL([]byte(`{"id":"T1","arrival_minute":10,"receiving_track":"R01","cars":[]}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "record tag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseJSONLRejectsLateHeader(t *testing.T) {
	stream := strings.Join([]string{
		`{"record":"train","id":"T1","arrival_minute":10,"receiving_track":"R01","cars":[{"mark":"UP","number":"1","kind":"boxcar","length_ft":60,"tare_tons":30,"gross_tons":100,"axles":4,"destination":"ALB"}]}`,
		`{"record":"order","order_id":"O1","yard_id":"WBKH"}`,
	}, "\n")
	_, err := parseJSONL([]byte(stream))
	if err == nil || !strings.Contains(err.Error(), "first record") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseJSONLRejectsUnknownRecordKind(t *testing.T) {
	_, err := parseJSONL([]byte(`{"record":"locomotive","id":"RD10"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown record kind") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCrossCheckRejectsForeignYard(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	order := res.Order
	order.YardID = "OTHER"
	findings := CrossCheck(cfg, order)
	if !HasErrors(findings) {
		t.Fatalf("expected an error finding, got %+v", findings)
	}
}

func TestCrossCheckRejectsUnknownReceivingTrack(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	order := res.Order
	order.Trains[0].ReceivingID = "R99"
	findings := CrossCheck(cfg, order)
	found := false
	for _, f := range findings {
		if f.Severity == config.SeverityError && strings.Contains(f.Message, "receiving_track") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a receiving track error, got %+v", findings)
	}
}

func TestCrossCheckDetectsReceivingOverflow(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := range cfg.Receiving {
		cfg.Receiving[i].CapacityFt = 200
	}
	findings := CrossCheck(cfg, res.Order)
	found := false
	for _, f := range findings {
		if f.Scope == "receiving_track" && strings.Contains(f.Message, "exceeds capacity") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a capacity error, got %+v", findings)
	}
}

func TestCrossCheckRejectsUndeclaredHazmatClass(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Hazmat.Classes = []string{"3"}
	findings := CrossCheck(cfg, res.Order)
	found := false
	for _, f := range findings {
		if f.Scope == "car" && strings.Contains(f.Message, "not declared") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an undeclared hazmat class error, got %+v", findings)
	}
}

func TestFindingsAreSorted(t *testing.T) {
	cfg := loadConfig(t)
	res, err := Load(examplePath("order.json"), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 1; i < len(res.Findings); i++ {
		a, b := res.Findings[i-1], res.Findings[i]
		if a.Severity > b.Severity {
			t.Fatal("findings are not sorted by severity")
		}
		if a.Severity == b.Severity && a.Scope > b.Scope {
			t.Fatal("findings are not sorted by scope")
		}
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	cfg := loadConfig(t)
	if _, err := Load(filepath.Join("..", "..", "examples", "nope.json"), cfg); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
