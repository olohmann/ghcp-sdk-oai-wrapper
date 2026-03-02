package config

import (
	"os"
	"strings"
)

// Config holds the application configuration.
type Config struct {
	// Port is the HTTP server listen port.
	Port string
	// APIKey is the optional Bearer token for authenticating API requests.
	// If empty, authentication is disabled.
	APIKey string
	// LogLevel controls logging verbosity ("debug", "info", "warn", "error").
	LogLevel string
	// CopilotCLIPath is the path to the Copilot CLI executable.
	CopilotCLIPath string
}

// Load reads configuration from environment variables.
func Load() *Config {
	return &Config{
		Port:           envOrDefault("PORT", "8080"),
		APIKey:         os.Getenv("API_KEY"),
		LogLevel:       strings.ToLower(envOrDefault("LOG_LEVEL", "info")),
		CopilotCLIPath: os.Getenv("COPILOT_CLI_PATH"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
