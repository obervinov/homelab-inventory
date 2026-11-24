// Package config loads the service configuration: who may report, how
// inventory items map to upstream repositories, and what to ignore.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Source is a host or collector allowed to report. Only the SHA-256 of its
// token is stored, so the config itself is not a secret.
type Source struct {
	Name        string `json:"name"`
	TokenSHA256 string `json:"token_sha256"`
}

// Rule matches inventory items by kind and name. An empty Kind matches any
// kind; Name supports "*" as a wildcard for any run of characters.
type Rule struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name"`
	Repo string `json:"repo,omitempty"`
}

type Kubernetes struct {
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
	Interval string `json:"interval"`
}

type Config struct {
	Sources         []Source   `json:"sources"`
	ReadTokenSHA256 string     `json:"read_token_sha256"`
	StaleAfter      string     `json:"stale_after"`
	Kubernetes      Kubernetes `json:"kubernetes"`
	Mappings        []Rule     `json:"mappings"`
	Ignore          []Rule     `json:"ignore"`

	staleAfter time.Duration
	interval   time.Duration
}

func (c *Config) StaleAfterDuration() time.Duration { return c.staleAfter }
func (c *Config) KubeInterval() time.Duration       { return c.interval }

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

func Parse(raw []byte) (*Config, error) {
	c := &Config{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if c.StaleAfter == "" {
		c.StaleAfter = "36h"
	}
	var err error
	if c.staleAfter, err = time.ParseDuration(c.StaleAfter); err != nil {
		return nil, fmt.Errorf("config: stale_after: %w", err)
	}
	if c.Kubernetes.Interval == "" {
		c.Kubernetes.Interval = "15m"
	}
	if c.interval, err = time.ParseDuration(c.Kubernetes.Interval); err != nil {
		return nil, fmt.Errorf("config: kubernetes.interval: %w", err)
	}
	if c.Kubernetes.Source == "" {
		c.Kubernetes.Source = "k8s"
	}
	if len(c.ReadTokenSHA256) != 64 {
		return nil, fmt.Errorf("config: read_token_sha256 must be a 64-char hex SHA-256")
	}
	seen := map[string]bool{c.Kubernetes.Source: c.Kubernetes.Enabled}
	for _, s := range c.Sources {
		if s.Name == "" || len(s.TokenSHA256) != 64 {
			return nil, fmt.Errorf("config: source %q needs a name and a 64-char hex token_sha256", s.Name)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("config: duplicate source %q", s.Name)
		}
		seen[s.Name] = true
	}
	for _, m := range c.Mappings {
		if m.Name == "" || m.Repo == "" {
			return nil, fmt.Errorf("config: mapping needs name and repo")
		}
	}
	return c, nil
}

// Classify returns the upstream repo and status for an item:
// "ignored", "mapped" (with repo) or "unmapped". Ignore rules win.
func (c *Config) Classify(kind, name string) (repo, status string) {
	for _, r := range c.Ignore {
		if r.matches(kind, name) {
			return "", "ignored"
		}
	}
	for _, r := range c.Mappings {
		if r.matches(kind, name) {
			return r.Repo, "mapped"
		}
	}
	return "", "unmapped"
}

func (r Rule) matches(kind, name string) bool {
	if r.Kind != "" && r.Kind != kind {
		return false
	}
	return Glob(r.Name, name)
}

// Glob reports whether s matches pattern, where "*" matches any run of
// characters including "/" (image names contain slashes).
func Glob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, last)
}
