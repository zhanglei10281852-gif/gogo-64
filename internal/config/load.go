package config

import (
	"fmt"
	"os"

	"HumpYard/internal/jsonx"
)

// MaxConfigBytes bounds the configuration document size.
const MaxConfigBytes = 8 << 20

// Load reads, strictly decodes, normalizes and validates a configuration file.
func Load(path string) (*Config, Report, error) {
	data, err := readLimited(path, MaxConfigBytes)
	if err != nil {
		return nil, Report{}, err
	}
	return Parse(data)
}

// Parse strictly decodes, normalizes and validates configuration bytes.
func Parse(data []byte) (*Config, Report, error) {
	var cfg Config
	if err := jsonx.DecodeStrict(data, &cfg); err != nil {
		return nil, Report{}, fmt.Errorf("decoding configuration: %w", err)
	}
	cfg.Normalize()
	report := Validate(&cfg)
	if err := report.Err(); err != nil {
		return nil, report, err
	}
	return &cfg, report, nil
}

// readLimited reads a file, refusing inputs larger than limit bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, expected a file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, limit is %d", path, info.Size(), limit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// ReadLimited exposes the bounded file reader to sibling packages.
func ReadLimited(path string, limit int64) ([]byte, error) {
	return readLimited(path, limit)
}
