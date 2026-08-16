package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplePath resolves a file in the repository examples directory.
func examplePath(name string) string {
	return filepath.Join("..", "..", "examples", name)
}

// invoke runs the CLI and returns the exit code with both streams.
func invoke(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUsageWithoutArguments(t *testing.T) {
	code, _, errOut := invoke()
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "usage:") {
		t.Fatalf("expected usage text, got %q", errOut)
	}
}

func TestHelpAndVersion(t *testing.T) {
	code, out, _ := invoke("help")
	if code != ExitOK || !strings.Contains(out, "validate") {
		t.Fatalf("help gave %d %q", code, out)
	}
	code, out, _ = invoke("--version")
	if code != ExitOK || !strings.Contains(out, Version) {
		t.Fatalf("version gave %d %q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := invoke("shove")
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}

func TestValidateRequiresConfig(t *testing.T) {
	code, _, errOut := invoke("validate")
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "-config is required") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}

func TestValidateRejectsUnknownFormat(t *testing.T) {
	code, _, errOut := invoke("validate", "-config", examplePath("config.json"), "-format", "yaml")
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "must be text or json") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}

func TestValidateRejectsExtraArgument(t *testing.T) {
	code, _, errOut := invoke("validate", "-config", examplePath("config.json"), "extra")
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "unexpected argument") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}

func TestValidateTextAndJSON(t *testing.T) {
	code, out, _ := invoke("validate", "-config", examplePath("config.json"))
	if code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(out, "Westbrook Hump Yard") {
		t.Fatalf("unexpected text output %q", out)
	}
	code, out, _ = invoke("validate", "-config", examplePath("config.json"), "-format", "json")
	if code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	var payload struct {
		Yard     string `json:"yard_id"`
		ConfigOK bool   `json:"config_ok"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json output: %v", err)
	}
	if payload.Yard != "WBKH" || !payload.ConfigOK {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestPlanningCommandsRun(t *testing.T) {
	for _, name := range []string{"block", "hump", "occupancy", "build", "rehandle"} {
		code, out, errOut := invoke(name, "-config", examplePath("config.json"), "-order", examplePath("order.json"))
		if code != ExitOK && code != ExitFindings {
			t.Fatalf("%s exited %d: %s", name, code, errOut)
		}
		if len(out) == 0 {
			t.Fatalf("%s produced no output", name)
		}
	}
}

func TestPlanningCommandsEmitValidJSON(t *testing.T) {
	for _, name := range []string{"block", "hump", "build", "rehandle"} {
		code, out, errOut := invoke(name, "-config", examplePath("config.json"),
			"-order", examplePath("order.json"), "-format", "json")
		if code != ExitOK && code != ExitFindings {
			t.Fatalf("%s exited %d: %s", name, code, errOut)
		}
		var any map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &any); err != nil {
			t.Fatalf("%s emitted invalid JSON: %v", name, err)
		}
	}
}

func TestPlanPersistsAndVerifies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	code, out, errOut := invoke("plan", "-config", examplePath("config.json"),
		"-order", examplePath("order.json"), "-store", dir, "-quiet")
	if code != ExitOK && code != ExitFindings {
		t.Fatalf("plan exited %d: %s", code, errOut)
	}
	if out != "" {
		t.Fatalf("quiet mode should print nothing, got %q", out)
	}
	for _, name := range []string{"meta.json", "plan.json", "ledger.jsonl", "audit.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing store file %s: %v", name, err)
		}
	}
	code, out, errOut = invoke("verify", "-store", dir)
	if code != ExitOK {
		t.Fatalf("verify exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "store ok") || !strings.Contains(out, "PROBLEMS") || strings.Contains(out, "hash-mismatch") {
		t.Fatalf("verify output %q", out)
	}
	code, out, _ = invoke("report", "-store", dir, "-section", "summary")
	if code != ExitOK && code != ExitFindings {
		t.Fatalf("report exited %d", code)
	}
	if !strings.Contains(out, "rehandle") {
		t.Fatalf("summary output %q", out)
	}
	code, out, _ = invoke("report", "-store", dir, "-section", "ledger")
	if code != ExitOK {
		t.Fatalf("ledger report exited %d", code)
	}
	if !strings.Contains(out, "WORK LEDGER") {
		t.Fatalf("ledger output %q", out)
	}
	code, out, _ = invoke("report", "-store", dir, "-section", "audit")
	if code != ExitOK {
		t.Fatalf("audit report exited %d", code)
	}
	if !strings.Contains(out, "AUDIT CHAIN") {
		t.Fatalf("audit output %q", out)
	}
	code, _, errOut = invoke("report", "-store", dir, "-section", "nope")
	if code != ExitUsage || !strings.Contains(errOut, "must be all") {
		t.Fatalf("unexpected section handling: %d %q", code, errOut)
	}
}

func TestPlanIsReproducibleOnDisk(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "one")
	second := filepath.Join(base, "two")
	for _, dir := range []string{first, second} {
		code, _, errOut := invoke("plan", "-config", examplePath("config.json"),
			"-order", examplePath("order.json"), "-store", dir, "-quiet")
		if code != ExitOK && code != ExitFindings {
			t.Fatalf("plan exited %d: %s", code, errOut)
		}
	}
	a, err := os.ReadFile(filepath.Join(first, "plan.json"))
	if err != nil {
		t.Fatalf("read first snapshot: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(second, "plan.json"))
	if err != nil {
		t.Fatalf("read second snapshot: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("two identical runs produced different snapshots")
	}
	la, err := os.ReadFile(filepath.Join(first, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("read first ledger: %v", err)
	}
	lb, err := os.ReadFile(filepath.Join(second, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("read second ledger: %v", err)
	}
	if string(la) != string(lb) {
		t.Fatal("two identical runs produced different ledgers")
	}
}

func TestIngestWritesAuditRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	code, out, errOut := invoke("ingest", "-config", examplePath("config.json"),
		"-order", examplePath("order.jsonl"), "-store", dir)
	if code != ExitOK {
		t.Fatalf("ingest exited %d: %s", code, errOut)
	}
	if !strings.Contains(out, "audit record 1 recorded") {
		t.Fatalf("unexpected output %q", out)
	}
	code, _, errOut = invoke("verify", "-store", dir)
	if code != ExitOK {
		t.Fatalf("verify exited %d: %s", code, errOut)
	}
}

func TestVerifyReportsTamperedStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	if code, _, errOut := invoke("plan", "-config", examplePath("config.json"),
		"-order", examplePath("order.json"), "-store", dir, "-quiet"); code == ExitUsage {
		t.Fatalf("plan failed: %s", errOut)
	}
	path := filepath.Join(dir, "audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	tampered := strings.Replace(string(data), `"action":"ingest"`, `"action":"ingested"`, 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	code, out, _ := invoke("verify", "-store", dir)
	if code != ExitFindings {
		t.Fatalf("verify exited %d", code)
	}
	if !strings.Contains(out, "hash-mismatch") {
		t.Fatalf("verify output %q", out)
	}
}

func TestMissingInputFile(t *testing.T) {
	code, _, errOut := invoke("hump", "-config", examplePath("config.json"), "-order", examplePath("nope.json"))
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "stat") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}

func TestReportRequiresSnapshot(t *testing.T) {
	code, _, errOut := invoke("report", "-store", filepath.Join(t.TempDir(), "empty"))
	if code != ExitUsage {
		t.Fatalf("exit code %d", code)
	}
	if !strings.Contains(errOut, "no plan snapshot") {
		t.Fatalf("unexpected stderr %q", errOut)
	}
}
