package config

import (
	"fmt"
	"os"
	"strings"
)

// Config holds all gateway configuration loaded from environment variables.
//
// Priority: system env vars > .env file > defaults in Load().
// Use env_loader.go (init()) to populate env vars from a .env file.
// Use Load() to read them into a Config struct.
type Config struct {
	SlackBotToken string // Required: Slack bot OAuth token (xoxb-...)
	SlackAppToken string // Required: Slack app-level token (xapp-...)
	PythonBin     string // Optional: Python interpreter path (default: "python3")
	AFCLIBin      string // Optional: AgentFactory CLI binary path
	AFWorkDir     string // Optional: Working directory for AF CLI and worker script
}

// Load reads environment variables and returns a populated Config.
func Load() Config {
	return Config{
		SlackBotToken: getEnv("SLACK_BOT_TOKEN", ""),
		SlackAppToken: getEnv("SLACK_APP_TOKEN", ""),
		PythonBin:     getEnv("PYTHON_BIN", "python3"),
		AFCLIBin:      getEnv("AF_CLI_BIN", ""),
		AFWorkDir:     getEnv("AF_WORK_DIR", ""),
	}
}

// Validate checks that all required configuration fields are set.
func (c Config) Validate() error {
	var missing []string
	if c.SlackBotToken == "" {
		missing = append(missing, "SLACK_BOT_TOKEN")
	}
	if c.SlackAppToken == "" {
		missing = append(missing, "SLACK_APP_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

// String returns a human-readable representation of the config with tokens redacted.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{SlackBotToken: %q, SlackAppToken: %q, PythonBin: %q, AFCLIBin: %q, AFWorkDir: %q}",
		redact(c.SlackBotToken),
		redact(c.SlackAppToken),
		c.PythonBin,
		c.AFCLIBin,
		c.AFWorkDir,
	)
}

func redact(s string) string {
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 8 {
		return "(redacted)"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// getEnv reads an environment variable, returning fallback if unset.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
