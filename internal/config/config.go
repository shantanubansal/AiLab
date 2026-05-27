// Package config loads service configuration from environment variables.
//
// Local development reads from .env (via the host shell). Production deploys
// inject env via Kubernetes secrets/configmaps. There is no config file path
// in v1; everything is env-driven so the same binary works in both worlds.
package config

import (
	"os"
	"strconv"
)

// API holds configuration for the api service.
type API struct {
	ListenAddr  string
	LogLevel    string
	DatabaseURL string
	NATSURL     string

	// WorkOS — when empty, api runs in dev-mode and accepts any bearer
	// token of the form "dev:<tenantId>:<userId>". Never enable in prod.
	WorkOSAPIKey   string
	WorkOSClientID string
	WorkOSJWKSURL  string

	// 32-byte AES key hex-encoded; used to encrypt at-rest secrets the
	// platform must read back in plaintext (e.g. webhook HMAC secrets).
	SecretsKeyHex string
}

// Controller holds configuration for the controller service.
type Controller struct {
	LeaderElect bool
	MetricsAddr string
}

// LoadAPI populates an API config from the environment.
func LoadAPI() API {
	return API{
		ListenAddr:     env("API_LISTEN_ADDR", ":8080"),
		LogLevel:       env("API_LOG_LEVEL", "info"),
		DatabaseURL:    env("DATABASE_URL", "postgres://ailab:ailab@localhost:5432/ailab?sslmode=disable"),
		NATSURL:        env("NATS_URL", "nats://localhost:4222"),
		WorkOSAPIKey:   os.Getenv("WORKOS_API_KEY"),
		WorkOSClientID: os.Getenv("WORKOS_CLIENT_ID"),
		WorkOSJWKSURL:  os.Getenv("WORKOS_JWKS_URL"),
		SecretsKeyHex:  env("API_SECRETS_KEY", "0000000000000000000000000000000000000000000000000000000000000000"),
	}
}

// LoadController populates a Controller config from the environment.
func LoadController() Controller {
	return Controller{
		LeaderElect: envBool("CONTROLLER_LEADER_ELECT", false),
		MetricsAddr: env("CONTROLLER_METRICS_ADDR", ":8081"),
	}
}

// DevMode reports whether the api should run with the dev-mode auth bypass.
func (a API) DevMode() bool { return a.WorkOSJWKSURL == "" }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
