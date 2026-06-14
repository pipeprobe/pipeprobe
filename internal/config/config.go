// Package config loads, validates, and exposes the application's runtime
// configuration.
package config

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the full runtime configuration tree.
type Config struct {
	App      App      `yaml:"app"`
	Log      Log      `yaml:"log"`
	Server   Server   `yaml:"server"`
	DB       DB       `yaml:"db"`
	Security Security `yaml:"security"`
}

// App holds service identity and the deployment environment.
type App struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Environment string `yaml:"environment"` // dev | prod
}

// Log configures the strucured logger.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // json | text
	Output string `yaml:"output"` // stdout | stderr | <file path>
}

// Server configures the HTTP server the the React UI talks to.
type Server struct {
	Host               string        `yaml:"host"`
	Port               int           `yaml:"port"`
	ReadTimeout        time.Duration `yaml:"read_timeout"`
	WriteTimeout       time.Duration `yaml:"write_timeout"`
	ShutdownTimeout    time.Duration `yaml:"shutdown_timeout"`
	CORSAllowedOrigins []string      `yaml:"cors_allowed_origins"`
}

// DB configures the PostgreSQL connection and pool.
type DB struct {
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	User            string        `yaml:"user"`
	Password        string        `yaml:"password"`
	Name            string        `yaml:"name"`
	SSLMode         string        `yaml:"sslmode"` // disable | require | verify-ca | verify-full
	MaxOpenConns    int           `yaml:"max_open_conns"`
	MaxIdleConns    int           `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime"`
}

// Security holds cryptographic material.
type Security struct {
	// CypherKey is the simmetric key used for at-rest encryption (e.g. probe
	// credentials stored in Postgres). For AES it must be 16, 24, or 32 bytes.
	CypherKey string `yaml:"cypher_key"`
}

// DSN builds a libpq connection string. It embeds the password, so the result
// must never be logged.
func (db DB) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		db.Host, db.Port, db.User, db.Password, db.Name, db.SSLMode,
	)
}

// LogValue implements slog.LogValuer so the password is never written to ligs,
// even if the whole DB struct is passed to a logger.
func (db DB) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", db.Host),
		slog.Int("port", db.Port),
		slog.String("user", db.User),
		slog.String("name", db.Name),
		slog.String("sslmode", db.SSLMode),
		slog.String("password", "[REDACTED]"),
	)
}

// LogValue redacts the cypher key from any log line.
func (s Security) LogValue() slog.Value {
	return slog.GroupValue(slog.String("cypher-key", "[REDACTED]"))
}

// Key decodes the base64-encoded cypher key into the raw bytes used by AES.
// Once Load succeeds, validation guarantees the length is 16, 24, or 32.
func (s Security) Key() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s.CypherKey)
	if err != nil {
		return nil, fmt.Errorf("decode cypher_key: %w", err)
	}
	return raw, nil
}

// Default returns a Config pre-filled with sensible defaults. Values present in
// the YAML file override these; absent ones survive.
func Default() *Config {
	return &Config{
		App: App{Environment: "dev"},
		Log: Log{Level: "info", Format: "json", Output: "stdout"},
		Server: Server{
			Host:            "0.0.0.0",
			Port:            8080,
			ReadTimeout:     10 * time.Second,
			WriteTimeout:    10 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
		DB: DB{
			Host:            "localhost",
			Port:            5432,
			SSLMode:         "disable",
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
	}
}

// Load reads, decodes, env-overrides, and validates the config at path.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyOverrides()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

const envPrefix = "PIPEPROBE_"

// applyOverrides lets environment variables override sensitive of
// deployment-specific fields. Env always wins over the YAML file, so secrets
// can stay out of version control (.env is gitignored; .env.example is not).
func (c *Config) applyOverrides() {
	setStr := func(key string, dst *string) {
		if v := os.Getenv(envPrefix + key); v != "" {
			*dst = v
		}
	}

	setInt := func(key string, dst *int) {
		if v := os.Getenv(envPrefix + key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}

	setInt("SERVER_PORT", &c.Server.Port)
	setStr("DB_HOST", &c.DB.Host)
	setInt("DB_PORT", &c.DB.Port)
	setStr("DB_USER", &c.DB.User)
	setStr("DB_PASSWORD", &c.DB.Password)
	setStr("DB_NAME", &c.DB.Name)
	setStr("SECURITY_CYPHER_KEY", &c.Security.CypherKey)
}
