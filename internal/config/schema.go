package config

import (
	"encoding/base64"
	"fmt"
	"strings"
)

var (
	validLogLevels  = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	validLogFormats = map[string]bool{"json": true, "text": true}
	validEnvs       = map[string]bool{"dev": true, "prod": true}
	validSLLModes   = map[string]bool{
		"disable": true, "allow": true, "prefer": true,
		"require": true, "verify-ca": true, "verify-full": true,
	}
)

// Validate checks the assembled configuration and returns the first problem
// found, with enought context to fix it. It runs after env overrides, so it
// validates the effective values rather than just what the file contained.
func (c *Config) Validate() error {
	// App
	if strings.TrimSpace(c.App.Name) == "" {
		return fmt.Errorf("app.name is required")
	}
	if !validEnvs[strings.ToLower(c.App.Environment)] {
		return fmt.Errorf("app.environment %q is not one of dev | prod", c.App.Environment)
	}

	// Log
	if !validLogLevels[strings.ToLower(c.Log.Level)] {
		return fmt.Errorf("log.level %q is not one of debug | info | warn | error", c.Log.Level)
	}
	if !validLogFormats[strings.ToLower(c.Log.Format)] {
		return fmt.Errorf("log.format %q is not one of json | text", c.Log.Format)
	}

	// Server
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port %d is out of range 1..65535", c.Server.Port)
	}

	// DB
	if strings.TrimSpace(c.DB.Host) == "" {
		return fmt.Errorf("db.host is required")
	}
	if c.DB.Port < 1 || c.DB.Port > 65535 {
		return fmt.Errorf("db.port %d is out of range 1..65535", c.DB.Port)
	}
	if strings.TrimSpace(c.DB.User) == "" {
		return fmt.Errorf("db.user is required")
	}
	if strings.TrimSpace(c.DB.Name) == "" {
		return fmt.Errorf("db.name is required")
	}
	if !validSLLModes[strings.ToLower(c.DB.SSLMode)] {
		return fmt.Errorf("db.sslmode %q is not a valid libpq sslmode", c.DB.SSLMode)
	}
	if c.DB.MaxIdleConns > c.DB.MaxOpenConns {
		return fmt.Errorf(
			"db.max_idle_conns (%d) cannot exceed db.max_open_conns (%d)",
			c.DB.MaxIdleConns, c.DB.MaxOpenConns,
		)
	}

	// Security
	if strings.TrimSpace(c.Security.CypherKey) == "" {
		return fmt.Errorf("security.cypher_key is required (set %sSECURITY_CYPHER_KEY)", envPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(c.Security.CypherKey)
	if err != nil {
		return fmt.Errorf("security.cypher_key must be base64-encoded (e.g. `openssl rand -base64 32`): %w", err)
	}
	if n := len(key); n != 16 && n != 24 && n != 32 {
		return fmt.Errorf("security.cypher_key must be 16, 24, or 32 bytes for AES, got %d", n)
	}

	return nil
}
