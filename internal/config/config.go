// Package config centralizes all environment-variable-driven configuration
// for the image-detection service. No secrets or environment-specific
// values (bucket names, project IDs, regions, credential paths, tokens)
// are hard-coded in source — everything comes from the process environment,
// typically populated from a local .env file (see .env.example).
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration for the service.
type Config struct {
	// HTTP server
	ListenAddr string

	// Auth
	APIBearerToken string // required; requests to /api/* must present this

	// Google Cloud Storage (temporary object storage for moderation)
	GCPProjectID       string
	GCSBucket          string
	GCSCredentialsFile string // path to service account JSON key
	SignedURLExpiry    time.Duration

	// Alibaba Cloud Content Moderation (Green/CIP)
	AlibabaAccessKeyID     string
	AlibabaAccessKeySecret string
	AlibabaRegionID        string

	// Logging
	LogsDir string
}

// Load reads configuration from environment variables. It does not read
// any .env file itself — call LoadDotEnv first if you want values from a
// .env file merged into the process environment.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:             envOrDefault("LISTEN_ADDR", "127.0.0.1:8080"),
		APIBearerToken:         os.Getenv("API_BEARER_TOKEN"),
		GCPProjectID:           os.Getenv("GCP_PROJECT_ID"),
		GCSBucket:              os.Getenv("GCS_BUCKET"),
		GCSCredentialsFile:     os.Getenv("GCS_CREDENTIALS_FILE"),
		AlibabaAccessKeyID:     os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"),
		AlibabaAccessKeySecret: os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"),
		AlibabaRegionID:        envOrDefault("ALIBABA_CLOUD_REGION_ID", "ap-southeast-1"),
		LogsDir:                envOrDefault("LOGS_DIR", "logs"),
	}

	expiryStr := envOrDefault("SIGNED_URL_EXPIRY_MINUTES", "10")
	minutes, err := parsePositiveMinutes(expiryStr)
	if err != nil {
		return nil, fmt.Errorf("config: SIGNED_URL_EXPIRY_MINUTES: %w", err)
	}
	cfg.SignedURLExpiry = time.Duration(minutes) * time.Minute

	return cfg, nil
}

// Validate checks that required configuration is present, returning a
// descriptive error listing everything missing (rather than failing on
// the first missing value) so operators can fix their .env in one pass.
func (c *Config) Validate() error {
	var missing []string

	if c.APIBearerToken == "" {
		missing = append(missing, "API_BEARER_TOKEN")
	}
	if c.GCPProjectID == "" {
		missing = append(missing, "GCP_PROJECT_ID")
	}
	if c.GCSBucket == "" {
		missing = append(missing, "GCS_BUCKET")
	}
	if c.GCSCredentialsFile == "" {
		missing = append(missing, "GCS_CREDENTIALS_FILE")
	}
	if c.AlibabaAccessKeyID == "" {
		missing = append(missing, "ALIBABA_CLOUD_ACCESS_KEY_ID")
	}
	if c.AlibabaAccessKeySecret == "" {
		missing = append(missing, "ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	}

	if len(missing) > 0 {
		return fmt.Errorf("config: missing required environment variables: %v (see .env.example)", missing)
	}
	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parsePositiveMinutes(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive, got %d", n)
	}
	return n, nil
}
