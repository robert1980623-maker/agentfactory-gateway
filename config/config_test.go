package config

import (
	"os"
	"strings"
	"testing"
)

func TestValidate_MissingRequired(t *testing.T) {
	cfg := Config{}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty config, got nil")
	}
	if !strings.Contains(err.Error(), "SLACK_BOT_TOKEN") {
		t.Errorf("expected error to mention SLACK_BOT_TOKEN, got: %v", err)
	}
	if !strings.Contains(err.Error(), "SLACK_APP_TOKEN") {
		t.Errorf("expected error to mention SLACK_APP_TOKEN, got: %v", err)
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := Config{
		SlackBotToken: "xoxb-test",
		SlackAppToken: "xapp-test",
	}
	err := cfg.Validate()
	if err != nil {
		t.Fatalf("expected no error for valid config, got: %v", err)
	}
}

func TestValidate_PartialMissing(t *testing.T) {
	cfg := Config{
		SlackBotToken: "xoxb-test",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when SLACK_APP_TOKEN is missing")
	}
	if strings.Contains(err.Error(), "SLACK_BOT_TOKEN") {
		t.Errorf("should not mention SLACK_BOT_TOKEN when it is set, got: %v", err)
	}
	if !strings.Contains(err.Error(), "SLACK_APP_TOKEN") {
		t.Errorf("expected error to mention SLACK_APP_TOKEN, got: %v", err)
	}
}

func TestString_RedactsTokens(t *testing.T) {
	cfg := Config{
		SlackBotToken: "xoxb-abcdef1234567890",
		SlackAppToken: "xapp-abcdef1234567890",
		PythonBin:     "python3",
	}
	s := cfg.String()
	if strings.Contains(s, "xoxb-abcdef1234567890") {
		t.Errorf("String() should redact SlackBotToken, got: %s", s)
	}
	if strings.Contains(s, "xapp-abcdef1234567890") {
		t.Errorf("String() should redact SlackAppToken, got: %s", s)
	}
	if !strings.Contains(s, "python3") {
		t.Errorf("String() should show PythonBin, got: %s", s)
	}
}

func TestString_EmptyTokens(t *testing.T) {
	cfg := Config{}
	s := cfg.String()
	if !strings.Contains(s, "(empty)") {
		t.Errorf("String() should show (empty) for blank tokens, got: %s", s)
	}
}

func TestLoad_FromEnv(t *testing.T) {
	os.Setenv("SLACK_BOT_TOKEN", "xoxb-from-env")
	os.Setenv("SLACK_APP_TOKEN", "xapp-from-env")
	os.Setenv("PYTHON_BIN", "python3.11")
	defer func() {
		os.Unsetenv("SLACK_BOT_TOKEN")
		os.Unsetenv("SLACK_APP_TOKEN")
		os.Unsetenv("PYTHON_BIN")
	}()

	cfg := Load()
	if cfg.SlackBotToken != "xoxb-from-env" {
		t.Errorf("expected SLACK_BOT_TOKEN from env, got: %s", cfg.SlackBotToken)
	}
	if cfg.SlackAppToken != "xapp-from-env" {
		t.Errorf("expected SLACK_APP_TOKEN from env, got: %s", cfg.SlackAppToken)
	}
	if cfg.PythonBin != "python3.11" {
		t.Errorf("expected PYTHON_BIN from env, got: %s", cfg.PythonBin)
	}
}

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("SLACK_BOT_TOKEN")
	os.Unsetenv("SLACK_APP_TOKEN")
	os.Unsetenv("PYTHON_BIN")

	cfg := Load()
	if cfg.PythonBin != "python3" {
		t.Errorf("expected default PythonBin 'python3', got: %s", cfg.PythonBin)
	}
}
