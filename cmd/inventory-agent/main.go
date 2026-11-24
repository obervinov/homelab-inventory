// inventory-agent reports the versions on this host to homelab-inventory.
// It runs once and exits, so a systemd timer or cron decides the schedule.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/obervinov/homelab-inventory/internal/agent"
	"github.com/obervinov/homelab-inventory/internal/store"
)

// version is set at build time (-X main.version=…).
var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/homelab-inventory-agent/config.json", "path to the agent config")
	dryRun := flag.Bool("dry-run", false, "print the report instead of sending it")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(*configPath, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "inventory-agent:", err)
		os.Exit(1)
	}
}

func run(configPath string, dryRun bool) error {
	cfg, err := agent.LoadConfig(configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	col := agent.New(cfg, version)
	col.Warn = func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "inventory-agent: skipped "+format+"\n", args...)
	}
	body, err := json.Marshal(struct {
		Items []store.Item `json:"items"`
	}{col.Collect(ctx)})
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Println(string(body))
		return nil
	}

	token := os.Getenv("INV_TOKEN")
	if token == "" {
		raw, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}
		token = strings.TrimSpace(string(raw))
	}
	if cfg.URL == "" || token == "" {
		return fmt.Errorf("url and token are required")
	}
	return send(ctx, cfg.URL, token, body)
}

func send(ctx context.Context, url, token string, body []byte) error {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err == nil {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return nil
			}
			last = fmt.Errorf("server answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
			// A rejected report will be rejected again; only a busy server is worth a retry.
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return last
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 5 * time.Second):
		}
	}
	return last
}
