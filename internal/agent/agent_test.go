package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fake(files map[string]string, cmds map[string]string) *Collector {
	c := New(&Config{}, "v1.2.3")
	c.ReadFile = func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
	c.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if v, ok := cmds[strings.Join(append([]string{name}, args...), " ")]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("not found")
	}
	return c
}

func TestCollect(t *testing.T) {
	c := fake(map[string]string{
		"/etc/os-release":            "NAME=\"Example\"\nID=example\nVERSION_ID=\"12\"\n",
		"/proc/sys/kernel/osrelease": "6.1.0-test\n",
	}, map[string]string{
		"docker ps --format {{.Image}}":     "example/app:1.0\nexample/app:1.0\nregistry.local:5000/x/y\n",
		"dpkg-query -W -f=${Version} pkg-a": "1.0-1",
		"apt-cache policy pkg-a":            "pkg-a:\n  Installed: 1.0-1\n  Candidate: 1.1-1\n",
		"dpkg-query -W -f=${Version} pkg-b": "2.0",
		"apt-cache policy pkg-b":            "pkg-b:\n  Installed: 2.0\n  Candidate: 2.0\n",
		"tool version":                      "tool v3.4.5 h1:abc\n",
		"other --version":                   "other build 77 (rev 9)\n",
	})
	c.Cfg.Docker = true
	c.Cfg.Packages = []string{"pkg-a", "pkg-b", "missing"}
	c.Cfg.Commands = []Command{
		{Name: "tool", Command: []string{"tool", "version"}},
		{Name: "other", Command: []string{"other", "--version"}, Pattern: `build (\d+)`},
		{Name: "silent", Command: []string{"tool", "version"}, Pattern: `nope (\d+)`},
	}
	var warns []string
	c.Warn = func(f string, a ...any) { warns = append(warns, f) }

	got := map[string]string{}
	for _, it := range c.Collect(context.Background()) {
		got[it.Kind+"/"+it.Name] = it.Version + "|" + it.Available
	}
	want := map[string]string{
		"binary/inventory-agent":         "1.2.3|",
		"os/example":                     "12|",
		"kernel/linux":                   "6.1.0-test|",
		"docker/example/app":             "1.0|",
		"docker/registry.local:5000/x/y": "latest|",
		"package/pkg-a":                  "1.0-1|1.1-1",
		"package/pkg-b":                  "2.0|",
		"binary/tool":                    "3.4.5|",
		"binary/other":                   "77|",
	}
	if len(got) != len(want) {
		t.Errorf("got %d items, want %d: %+v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(warns) != 2 {
		t.Errorf("want warnings for the missing package and the unmatched command, got %v", warns)
	}
}

// A host with nothing readable still produces the agent's own version.
func TestCollectOnEmptyHost(t *testing.T) {
	c := fake(nil, nil)
	c.Cfg.Docker = true
	items := c.Collect(context.Background())
	if len(items) != 1 || items[0].Name != "inventory-agent" {
		t.Errorf("%+v", items)
	}
}

func TestLoadConfigRejects(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown.json": `{"url":"x","nope":1}`,
		"noname.json":  `{"commands":[{"command":["a"]}]}`,
		"badre.json":   `{"commands":[{"name":"a","command":["a"],"pattern":"("}]}`,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
