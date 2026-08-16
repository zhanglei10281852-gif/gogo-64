// Package jsonx provides strict JSON and JSONL decoding helpers used across
// HumpYard. Strict means: unknown object members are rejected, and a document
// must not contain trailing values after the first top-level value.
package jsonx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// MaxLineBytes bounds a single JSONL record so that a malformed input cannot
// force unbounded buffering.
const MaxLineBytes = 1 << 20

// DecodeStrict decodes exactly one JSON value from data into out. Unknown
// fields cause an error, and any trailing non-whitespace content is rejected.
func DecodeStrict(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := decodeInto(dec, out); err != nil {
		return err
	}
	return ensureNoTrailing(dec)
}

// decodeInto performs the decode step, restoring standard number handling for
// the target value. UseNumber is enabled on the probing decoder only to keep
// error messages stable; the real decode uses a second decoder over the same
// bytes so numeric conversions behave normally.
func decodeInto(dec *json.Decoder, out any) error {
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty document: expected a JSON value")
		}
		return fmt.Errorf("malformed JSON: %w", err)
	}
	inner := json.NewDecoder(bytes.NewReader(raw))
	inner.DisallowUnknownFields()
	if err := inner.Decode(out); err != nil {
		return translate(err)
	}
	return ensureNoTrailing(inner)
}

// ensureNoTrailing verifies the decoder stream has been fully consumed.
func ensureNoTrailing(dec *json.Decoder) error {
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("trailing content after JSON value: %w", err)
		}
		return fmt.Errorf("trailing content after JSON value: %s", compact(extra))
	}
	return nil
}

// compact renders a raw message on a single line for use in error messages.
func compact(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	s := buf.String()
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// translate rewrites decoder errors into messages that name the offending
// field or type in a stable way.
func translate(err error) error {
	var ute *json.UnmarshalTypeError
	if ok := asUnmarshalTypeError(err, &ute); ok {
		if ute.Field != "" {
			return fmt.Errorf("field %q: cannot decode %s into %s", ute.Field, ute.Value, ute.Type)
		}
		return fmt.Errorf("cannot decode %s into %s", ute.Value, ute.Type)
	}
	return fmt.Errorf("invalid JSON: %w", err)
}

// asUnmarshalTypeError is a tiny helper so translate stays readable.
func asUnmarshalTypeError(err error, target **json.UnmarshalTypeError) bool {
	if ute, ok := err.(*json.UnmarshalTypeError); ok {
		*target = ute
		return true
	}
	return false
}

// DecodeLoose decodes one JSON value while tolerating unknown members. It is
// used only to peek at a discriminator field before the strict decode of the
// full record.
func DecodeLoose(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("malformed JSON: %w", err)
	}
	return ensureNoTrailing(dec)
}

// LineRecord is a single decoded JSONL record together with its 1-based line
// number, which callers use for diagnostics.
type LineRecord struct {
	Line int
	Raw  json.RawMessage
}

// SplitJSONL splits a JSONL document into raw records. Blank lines are
// skipped; lines beginning with '#' are treated as comments. A record longer
// than MaxLineBytes is rejected.
func SplitJSONL(data []byte) ([]LineRecord, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), MaxLineBytes)
	var out []LineRecord
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		out = append(out, LineRecord{Line: line, Raw: json.RawMessage(text)})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading JSONL: %w", err)
	}
	return out, nil
}

// DecodeRecord strictly decodes one JSONL record into out.
func DecodeRecord(rec LineRecord, out any) error {
	if err := DecodeStrict(rec.Raw, out); err != nil {
		return fmt.Errorf("line %d: %w", rec.Line, err)
	}
	return nil
}

// MarshalCanonical renders v as compact JSON with a trailing newline and with
// HTML escaping disabled, which keeps stored bytes stable across runs.
func MarshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// MarshalIndent renders v as two-space indented JSON with a trailing newline.
func MarshalIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// SortedKeys returns the keys of m in lexical order so callers never iterate a
// map in random order when producing output.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
