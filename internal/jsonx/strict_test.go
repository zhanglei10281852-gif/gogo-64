package jsonx

import (
	"strings"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeStrictAcceptsExactDocument(t *testing.T) {
	var got sample
	if err := DecodeStrict([]byte(`{"name":"hump","count":3}`), &got); err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	if got.Name != "hump" || got.Count != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeStrictRejectsUnknownField(t *testing.T) {
	var got sample
	err := DecodeStrict([]byte(`{"name":"hump","retarder":"light"}`), &got)
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
	if !strings.Contains(err.Error(), "retarder") {
		t.Fatalf("error should name the unknown field, got %v", err)
	}
}

func TestDecodeStrictRejectsTrailingValue(t *testing.T) {
	var got sample
	err := DecodeStrict([]byte(`{"name":"a","count":1} {"name":"b","count":2}`), &got)
	if err == nil {
		t.Fatal("expected an error for a trailing value")
	}
	if !strings.Contains(err.Error(), "trailing content") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeStrictRejectsEmptyDocument(t *testing.T) {
	var got sample
	if err := DecodeStrict(nil, &got); err == nil {
		t.Fatal("expected an error for an empty document")
	}
}

func TestDecodeStrictReportsTypeMismatch(t *testing.T) {
	var got sample
	err := DecodeStrict([]byte(`{"count":"three"}`), &got)
	if err == nil {
		t.Fatal("expected a type error")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Fatalf("error should name the field, got %v", err)
	}
}

func TestSplitJSONLSkipsBlanksAndComments(t *testing.T) {
	data := []byte("# header\n\n{\"name\":\"a\",\"count\":1}\n{\"name\":\"b\",\"count\":2}\n")
	records, err := SplitJSONL(data)
	if err != nil {
		t.Fatalf("SplitJSONL: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Line != 3 || records[1].Line != 4 {
		t.Fatalf("unexpected line numbers %d and %d", records[0].Line, records[1].Line)
	}
	var first sample
	if err := DecodeRecord(records[0], &first); err != nil {
		t.Fatalf("DecodeRecord: %v", err)
	}
	if first.Name != "a" {
		t.Fatalf("got %+v", first)
	}
}

func TestDecodeRecordReportsLineNumber(t *testing.T) {
	records, err := SplitJSONL([]byte("{\"name\":\"a\",\"count\":1}\n{\"nope\":1}\n"))
	if err != nil {
		t.Fatalf("SplitJSONL: %v", err)
	}
	var out sample
	err = DecodeRecord(records[1], &out)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("expected a line 2 error, got %v", err)
	}
}

func TestMarshalCanonicalIsStable(t *testing.T) {
	value := map[string]int{"c": 3, "a": 1, "b": 2}
	first, err := MarshalCanonical(value)
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := MarshalCanonical(value)
		if err != nil {
			t.Fatalf("MarshalCanonical: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("encoding is not stable: %q then %q", first, again)
		}
	}
	if want := "{\"a\":1,\"b\":2,\"c\":3}\n"; string(first) != want {
		t.Fatalf("got %q want %q", first, want)
	}
}

func TestDecodeLooseIgnoresUnknownFields(t *testing.T) {
	var probe struct {
		Name string `json:"name"`
	}
	if err := DecodeLoose([]byte(`{"name":"x","extra":true}`), &probe); err != nil {
		t.Fatalf("DecodeLoose: %v", err)
	}
	if probe.Name != "x" {
		t.Fatalf("got %+v", probe)
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]int{"z": 1, "a": 2, "m": 3})
	want := []string{"a", "m", "z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
