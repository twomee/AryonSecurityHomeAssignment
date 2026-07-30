package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := load(func(string) (string, bool) { return "", false })

	if err == nil {
		t.Fatal("load() error = nil, want missing DATABASE_URL error")
	}
}

func TestLoadUsesSafeDefaults(t *testing.T) {
	values := map[string]string{"DATABASE_URL": "postgresql://example"}
	got, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})

	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if got.Port != "8080" || got.MaxBodyBytes != 10<<20 {
		t.Fatalf("load() defaults = %#v", got)
	}
	if got.MaxNodes != 100_000 || got.MaxDepth != 1_000 {
		t.Fatalf("load() hierarchy limits = %#v", got)
	}
	if got.ReadTimeout != 15*time.Second || got.ShutdownTimeout != 30*time.Second {
		t.Fatalf("load() timeout defaults = %#v", got)
	}
	if got.OperationTimeout != 20*time.Second || got.DatabaseLockTimeout != 5*time.Second {
		t.Fatalf("load() operation timeout defaults = %#v", got)
	}
}

func TestLoadRejectsInvalidNumericConfiguration(t *testing.T) {
	values := map[string]string{
		"DATABASE_URL":        "postgresql://example",
		"HIERARCHY_MAX_NODES": "not-a-number",
	}

	_, err := load(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})

	if err == nil {
		t.Fatal("load() error = nil, want invalid HIERARCHY_MAX_NODES error")
	}
}
