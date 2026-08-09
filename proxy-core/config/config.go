package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"time"
)

const (
	Version = "0.1.0"
)

type Config struct {
	APIBind          string
	DBPath           string
	HTTPProxyBind    string
	SOCKSProxyBind   string
	SessionToken     string
	CheckTarget      string
	CheckTimeout     time.Duration
	CheckConcurrency int
	RefreshInterval  time.Duration
}

func New() *Config {
	c := &Config{
		APIBind:          "127.0.0.1:17890",
		DBPath:           "proxypilot.db",
		HTTPProxyBind:    "127.0.0.1:7890",
		SOCKSProxyBind:   "127.0.0.1:7891",
		CheckTarget:      "http://www.gstatic.com/generate_204",
		CheckTimeout:     10 * time.Second,
		CheckConcurrency: 32,
		RefreshInterval:  15 * time.Minute,
	}
	c.SessionToken = generatedSessionToken()
	c.ApplyEnv()
	return c
}

func (c *Config) ApplyEnv() {
	if v := os.Getenv("PROXYPILOT_API_BIND"); v != "" {
		c.APIBind = v
	}
	if v := os.Getenv("PROXYPILOT_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("PROXYPILOT_HTTP_BIND"); v != "" {
		c.HTTPProxyBind = v
	}
	if v := os.Getenv("PROXYPILOT_SOCKS5_BIND"); v != "" {
		c.SOCKSProxyBind = v
	}
	if v := os.Getenv("PROXYPILOT_TOKEN"); v != "" {
		c.SessionToken = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TARGET"); v != "" {
		c.CheckTarget = v
	}
	if v := os.Getenv("PROXYPILOT_CHECK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.CheckTimeout = d
		}
	}
	if v := os.Getenv("PROXYPILOT_CHECK_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CheckConcurrency = n
		}
	}
	if v := os.Getenv("PROXYPILOT_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.RefreshInterval = d
		}
	}
}

func generatedSessionToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-session-token"
	}
	return hex.EncodeToString(b)
}