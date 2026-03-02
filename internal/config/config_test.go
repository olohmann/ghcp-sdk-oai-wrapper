package config_test

import (
	"os"
	"testing"

	"github.com/olohmann/ghcp-sdk-oai-wrapper/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Unset env vars that might interfere
	os.Unsetenv("PORT")
	os.Unsetenv("API_KEY")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("COPILOT_CLI_PATH")

	cfg := config.Load()

	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.APIKey != "" {
		t.Errorf("expected empty API key, got %s", cfg.APIKey)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected info log level, got %s", cfg.LogLevel)
	}
	if cfg.CopilotCLIPath != "" {
		t.Errorf("expected empty CLI path, got %s", cfg.CopilotCLIPath)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	os.Setenv("PORT", "9090")
	os.Setenv("API_KEY", "test-key")
	os.Setenv("LOG_LEVEL", "DEBUG")
	os.Setenv("COPILOT_CLI_PATH", "/usr/bin/copilot")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("API_KEY")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("COPILOT_CLI_PATH")
	}()

	cfg := config.Load()

	if cfg.Port != "9090" {
		t.Errorf("expected 9090, got %s", cfg.Port)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("expected test-key, got %s", cfg.APIKey)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug (lowercased), got %s", cfg.LogLevel)
	}
	if cfg.CopilotCLIPath != "/usr/bin/copilot" {
		t.Errorf("expected /usr/bin/copilot, got %s", cfg.CopilotCLIPath)
	}
}
