package config

import (
	"os"
	"strconv"
	"strings"
	"time"
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
	// GitHubToken is a GitHub token with Copilot scope for headless authentication.
	GitHubToken string
	// Mode selects the API surface: "openai" or "ollama".
	Mode string
	// ToolSessionTTL bounds how long a parked tool session (a live Copilot
	// session awaiting the client's tool results between turn-1 and turn-2 of an
	// OpenAI tool round-trip) is retained before it is evicted and torn down.
	ToolSessionTTL time.Duration
	// ToolSessionMaxParked caps the number of concurrently parked tool sessions.
	// When the cap is reached the oldest parked session is evicted.
	ToolSessionMaxParked int
}

// Load reads configuration from environment variables.
func Load() *Config {
	return &Config{
		Port:                 envOrDefault("PORT", "8080"),
		APIKey:               os.Getenv("API_KEY"),
		LogLevel:             strings.ToLower(envOrDefault("LOG_LEVEL", "info")),
		CopilotCLIPath:       os.Getenv("COPILOT_CLI_PATH"),
		GitHubToken:          os.Getenv("GITHUB_TOKEN"),
		Mode:                 strings.ToLower(envOrDefault("MODE", "openai")),
		ToolSessionTTL:       envDurationOrDefault("TOOL_SESSION_TTL", 10*time.Minute),
		ToolSessionMaxParked: envIntOrDefault("TOOL_SESSION_MAX_PARKED", 256),
	}
}

// ApplyFlags overrides configuration with CLI flag values (if non-empty).
func (c *Config) ApplyFlags(flagPort, flagMode string) {
	if flagPort != "" {
		c.Port = flagPort
	}
	if flagMode != "" {
		c.Mode = strings.ToLower(flagMode)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envDurationOrDefault parses a Go duration string (e.g. "10m", "30s") from the
// environment, falling back to the default on absence or parse error.
func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// envIntOrDefault parses a positive integer from the environment, falling back
// to the default on absence, parse error, or non-positive value.
func envIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
