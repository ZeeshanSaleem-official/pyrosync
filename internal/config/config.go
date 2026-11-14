package config

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds validated runtime configuration loaded from environment variables.
type Config struct {
	DatabaseURL       string
	FirmsMapKey       string
	AdminToken        string
	HTTPAddr          string
	AllowedOrigins    []string
	IngestIntervalMin int
	LogLevel          string
}

// Load reads an optional local .env file (without overriding exported variables)
// and constructs a validated Config from the process environment.
func Load(envFiles ...string) (Config, error) {
	for _, path := range envFiles {
		if path == "" {
			continue
		}
		if err := loadDotEnvIfExists(path); err != nil {
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
	}

	cfg := Config{
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		FirmsMapKey:       strings.TrimSpace(os.Getenv("FIRMS_MAP_KEY")),
		AdminToken:        strings.TrimSpace(os.Getenv("ADMIN_TOKEN")),
		HTTPAddr:          envOr("HTTP_ADDR", ":8080"),
		AllowedOrigins:    splitCSV(os.Getenv("ALLOWED_ORIGINS")),
		IngestIntervalMin: 60,
		LogLevel:          strings.ToLower(envOr("LOG_LEVEL", "info")),
	}

	if raw := strings.TrimSpace(os.Getenv("INGEST_INTERVAL_MIN")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			return Config{}, fmt.Errorf("invalid INGEST_INTERVAL_MIN %q: must be an integer >= 1", raw)
		}
		cfg.IngestIntervalMin = v
	}

	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return Config{}, fmt.Errorf("invalid LOG_LEVEL %q: must be debug, info, warn, or error", cfg.LogLevel)
	}

	return cfg, nil
}

// RequireDB validates that DATABASE_URL is configured.
func (c Config) RequireDB() error {
	if c.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	return nil
}

// SlogLevel converts LogLevel to an slog.Level.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Redact replaces secret tokens in s with [REDACTED] so errors and logs never leak keys.
func (c Config) Redact(s string) string {
	out := s
	if c.FirmsMapKey != "" {
		out = strings.ReplaceAll(out, c.FirmsMapKey, "[REDACTED]")
	}
	if c.AdminToken != "" {
		out = strings.ReplaceAll(out, c.AdminToken, "[REDACTED]")
	}
	return out
}

// LogValue implements slog.LogValuer with masked credentials.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("database_url", maskDSN(c.DatabaseURL)),
		slog.Bool("firms_map_key_set", c.FirmsMapKey != ""),
		slog.Bool("admin_token_set", c.AdminToken != ""),
		slog.String("http_addr", c.HTTPAddr),
		slog.Int("ingest_interval_min", c.IngestIntervalMin),
		slog.String("log_level", c.LogLevel),
	)
}

func maskDSN(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "[INVALID_DSN]"
	}
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func loadDotEnvIfExists(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
