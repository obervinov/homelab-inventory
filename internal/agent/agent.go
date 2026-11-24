// Package agent collects the versions present on one host: the OS release, the
// kernel, running container images, chosen packages and chosen binaries. Every
// source is optional, so the same binary serves a container host, a package-only
// box and a hypervisor.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/obervinov/homelab-inventory/internal/kube"
	"github.com/obervinov/homelab-inventory/internal/store"
)

// Command describes a binary whose version is read from its own output.
type Command struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	// Pattern is a regular expression; the first capture group is the version.
	// Empty means the first x.y.z found in the output.
	Pattern string `json:"pattern,omitempty"`
}

type Config struct {
	URL string `json:"url"`
	// TokenFile holds the bearer token (0600, one line). INV_TOKEN overrides it.
	TokenFile string    `json:"token_file"`
	Docker    bool      `json:"docker"`
	Packages  []string  `json:"packages"`
	Commands  []Command `json:"commands"`
}

func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, cmd := range c.Commands {
		if cmd.Name == "" || len(cmd.Command) == 0 {
			return nil, fmt.Errorf("config: a command needs a name and a command line")
		}
		if _, err := regexp.Compile(cmd.Pattern); err != nil {
			return nil, fmt.Errorf("config: command %q: %w", cmd.Name, err)
		}
	}
	return c, nil
}

// Collector gathers items. Run and ReadFile are replaceable so the collection
// logic is testable without a real host.
type Collector struct {
	Cfg      *Config
	Version  string
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
	ReadFile func(path string) ([]byte, error)
	// Warn receives one line per source that could not be read.
	Warn func(format string, args ...any)
}

func New(cfg *Config, version string) *Collector {
	return &Collector{
		Cfg:      cfg,
		Version:  version,
		ReadFile: os.ReadFile,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		Warn: func(string, ...any) {},
	}
}

var defaultVersion = regexp.MustCompile(`(\d+\.\d+\.\d+(?:[-+.][0-9A-Za-z.+-]+)?)`)

// Collect never fails as a whole: a source that cannot be read is reported
// through Warn and left out, the rest is still sent.
func (c *Collector) Collect(ctx context.Context) []store.Item {
	var items []store.Item
	add := func(it store.Item) {
		if it.Name != "" && it.Version != "" {
			items = append(items, it)
		}
	}

	if c.Version != "" {
		add(store.Item{Kind: "binary", Name: "inventory-agent", Version: strings.TrimPrefix(c.Version, "v")})
	}

	if raw, err := c.ReadFile("/etc/os-release"); err == nil {
		rel := parseOSRelease(string(raw))
		add(store.Item{Kind: "os", Name: rel["ID"], Version: rel["VERSION_ID"]})
	} else {
		c.Warn("os-release: %v", err)
	}

	if raw, err := c.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		add(store.Item{Kind: "kernel", Name: "linux", Version: strings.TrimSpace(string(raw))})
	} else {
		c.Warn("kernel: %v", err)
	}

	if c.Cfg.Docker {
		if out, err := c.Run(ctx, "docker", "ps", "--format", "{{.Image}}"); err == nil {
			seen := map[string]bool{}
			for _, ref := range strings.Fields(string(out)) {
				name, version := kube.ParseImage(ref)
				if seen[name+"\x00"+version] {
					continue
				}
				seen[name+"\x00"+version] = true
				add(store.Item{Kind: "docker", Name: name, Version: version})
			}
		} else {
			c.Warn("docker: %v", err)
		}
	}

	for _, pkg := range c.Cfg.Packages {
		out, err := c.Run(ctx, "dpkg-query", "-W", "-f=${Version}", pkg)
		if err != nil {
			c.Warn("package %s: %v", pkg, err)
			continue
		}
		it := store.Item{Kind: "package", Name: pkg, Version: strings.TrimSpace(string(out))}
		// The candidate is what apt would install now; equal to the installed
		// version when the host is up to date, so it is sent only when it differs.
		if policy, err := c.Run(ctx, "apt-cache", "policy", pkg); err == nil {
			if cand := aptCandidate(string(policy)); cand != "" && cand != "(none)" && cand != it.Version {
				it.Available = cand
			}
		}
		add(it)
	}

	for _, cmd := range c.Cfg.Commands {
		out, err := c.Run(ctx, cmd.Command[0], cmd.Command[1:]...)
		if err != nil {
			c.Warn("command %s: %v", cmd.Name, err)
			continue
		}
		re := defaultVersion
		if cmd.Pattern != "" {
			re = regexp.MustCompile(cmd.Pattern)
		}
		m := re.FindSubmatch(out)
		if len(m) < 2 {
			c.Warn("command %s: no version in its output", cmd.Name)
			continue
		}
		add(store.Item{Kind: "binary", Name: cmd.Name, Version: string(m[1])})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Kind+items[i].Name < items[j].Kind+items[j].Name })
	return items
}

func parseOSRelease(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			out[k] = strings.Trim(v, `"'`)
		}
	}
	return out
}

func aptCandidate(policy string) string {
	for _, line := range strings.Split(policy, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
