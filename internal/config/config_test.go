package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactAndMasking(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://pyrosync:supersecret@localhost:5432/pyrosync?sslmode=disable",
		FirmsMapKey: "MY_SECRET_MAP_KEY_12345",
		AdminToken:  "ADMIN_SECRET_TOKEN_XYZ",
	}

	errMsg := "GET https://firms.modaps.eosdis.nasa.gov/api/area/csv/MY_SECRET_MAP_KEY_12345/MODIS_NRT/world/1 failed; bearer ADMIN_SECRET_TOKEN_XYZ"
	redacted := cfg.Redact(errMsg)
	if strings.Contains(redacted, cfg.FirmsMapKey) {
		t.Fatalf("redacted message leaked FirmsMapKey: %s", redacted)
	}
	if strings.Contains(redacted, cfg.AdminToken) {
		t.Fatalf("redacted message leaked AdminToken: %s", redacted)
	}

	logged := cfg.LogValue().String()
	if strings.Contains(logged, "supersecret") || strings.Contains(logged, cfg.FirmsMapKey) || strings.Contains(logged, cfg.AdminToken) {
		t.Fatalf("LogValue leaked credentials: %s", logged)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# comment\nDATABASE_URL=\"postgres://user:pass@localhost:5432/db\"\nINGEST_INTERVAL_MIN=30\nLOG_LEVEL=debug\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DATABASE_URL", "")
	// Ensure Load reads values when env keys are unset.
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Unsetenv("INGEST_INTERVAL_MIN")
	_ = os.Unsetenv("LOG_LEVEL")

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/db" {
		t.Fatalf("unexpected DatabaseURL: %q", cfg.DatabaseURL)
	}
	if cfg.IngestIntervalMin != 30 {
		t.Fatalf("expected IngestIntervalMin=30, got %d", cfg.IngestIntervalMin)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("expected LogLevel=debug, got %q", cfg.LogLevel)
	}
}
