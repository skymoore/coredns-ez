package main

import (
	"encoding/json"
	"fmt"

	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const (
	solverName = "coredns"
	defaultTTL = 60
)

// secretRef is a Secret key, optionally in another namespace.
type secretRef struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Namespace string `json:"namespace,omitempty"`
}

// solverConfig is the Issuer/ClusterIssuer webhook.config JSON.
type solverConfig struct {
	ServerURL string `json:"serverUrl"`
	// TokenSecretRef is the API token (operator+) minted in coredns-ez.
	TokenSecretRef secretRef `json:"tokenSecretRef"`
	// AuthTokenSecretRef is an alias for TokenSecretRef (same shape as the Technitium webhook).
	AuthTokenSecretRef secretRef `json:"authTokenSecretRef"`
	Zone               string    `json:"zone,omitempty"`
	TTL                int       `json:"ttl,omitempty"`
}

func (c solverConfig) secret() secretRef {
	if c.TokenSecretRef.Name != "" {
		return c.TokenSecretRef
	}
	return c.AuthTokenSecretRef
}

func (c solverConfig) ttl() int {
	if c.TTL > 0 {
		return c.TTL
	}
	return defaultTTL
}

func loadConfig(raw *extapi.JSON) (solverConfig, error) {
	cfg := solverConfig{}
	if raw == nil || len(raw.Raw) == 0 || string(raw.Raw) == "null" {
		return cfg, fmt.Errorf("webhook config is required (serverUrl, tokenSecretRef)")
	}
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("decode webhook config: %w", err)
	}
	if cfg.ServerURL == "" {
		return cfg, fmt.Errorf("serverUrl is required")
	}
	sec := cfg.secret()
	if sec.Name == "" || sec.Key == "" {
		return cfg, fmt.Errorf("tokenSecretRef.name and tokenSecretRef.key are required")
	}
	return cfg, nil
}
