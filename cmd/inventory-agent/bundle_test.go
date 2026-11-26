package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The release bundle is what hosts install from, so the test builds one the way the
// release job does, checks what is inside, and runs its installer into a staging root.
func TestAgentBundleInstalls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bundle is built and installed with POSIX sh")
	}
	const version = "v9.9.9"
	dist := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		bin := filepath.Join(dist, "inventory-agent_"+version+"_linux_"+arch)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+arch+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Lines this script does not own must survive; a bundle line from an earlier run must not.
	sums := "0000  inventory-agent_" + version + "_linux_amd64\n" +
		"eeee  something-else.tar.gz\n" +
		"ffff  inventory-agent_" + version + "_linux_amd64.tar.gz\n"
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	runCmd(t, "", "sh", "../../deploy/agent/bundle.sh", version, dist)

	raw, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("unexpected SHA256SUMS line %q", line)
		}
		listed[strings.TrimPrefix(f[1], "*")] = f[0]
	}
	if listed["inventory-agent_"+version+"_linux_amd64"] != "0000" {
		t.Error("the binary's checksum line was dropped from SHA256SUMS")
	}
	if listed["something-else.tar.gz"] != "eeee" {
		t.Error("the checksum line of an unrelated archive was dropped from SHA256SUMS")
	}
	if listed["inventory-agent_"+version+"_linux_amd64.tar.gz"] == "ffff" {
		t.Error("a bundle line from an earlier run was kept in SHA256SUMS")
	}

	for _, arch := range []string{"amd64", "arm64"} {
		name := "inventory-agent_" + version + "_linux_" + arch
		archive := filepath.Join(dist, name+".tar.gz")
		data, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); listed[name+".tar.gz"] != got {
			t.Errorf("%s: SHA256SUMS has %q, the archive is %q", name, listed[name+".tar.gz"], got)
		}

		out := t.TempDir()
		modes := extract(t, archive, out)
		want := map[string]os.FileMode{
			name + "/inventory-agent":         0o755,
			name + "/install.sh":              0o755,
			name + "/inventory-agent.service": 0o644,
			name + "/inventory-agent.timer":   0o644,
			name + "/config.example.json":     0o644,
		}
		for path, mode := range want {
			if modes[path] != mode {
				t.Errorf("%s: mode %o in the archive, want %o", path, modes[path], mode)
			}
		}
		if len(modes) != len(want) {
			t.Errorf("%s: archive holds %d files, want %d: %v", name, len(modes), len(want), modes)
		}

		root := t.TempDir()
		bundle := filepath.Join(out, name)
		cmd := exec.Command("sh", filepath.Join(bundle, "install.sh"), "--config", filepath.Join(bundle, "config.example.json"))
		cmd.Env = append(os.Environ(), "INSTALL_ROOT="+root)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install.sh: %v\n%s", err, b)
		}
		installed := map[string]os.FileMode{
			"usr/local/bin/inventory-agent":              0o755,
			"etc/homelab-inventory-agent":                0o700,
			"etc/homelab-inventory-agent/config.json":    0o600,
			"etc/systemd/system/inventory-agent.service": 0o644,
			"etc/systemd/system/inventory-agent.timer":   0o644,
		}
		for path, mode := range installed {
			st, err := os.Stat(filepath.Join(root, path))
			if err != nil {
				t.Errorf("%s: %v", path, err)
				continue
			}
			if st.Mode().Perm() != mode {
				t.Errorf("%s: installed with mode %o, want %o", path, st.Mode().Perm(), mode)
			}
		}
		token := filepath.Join(root, "etc/homelab-inventory-agent/token")
		if _, err := os.Stat(token); err == nil {
			t.Error("install.sh wrote a token file; the token is the host's to provide")
		}

		// A second run without --config, over files left too open by a manual install:
		// the config and the token stay, with their modes put right.
		cfg := filepath.Join(root, "etc/homelab-inventory-agent/config.json")
		if err := os.WriteFile(token, []byte("secret\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{cfg, token} {
			if err := os.Chmod(f, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		again := exec.Command("sh", filepath.Join(bundle, "install.sh"))
		again.Env = append(os.Environ(), "INSTALL_ROOT="+root)
		if b, err := again.CombinedOutput(); err != nil {
			t.Fatalf("install.sh, second run: %v\n%s", err, b)
		}
		for _, f := range []string{cfg, token} {
			st, err := os.Stat(f)
			if err != nil {
				t.Errorf("second run removed %s: %v", f, err)
				continue
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %o after the second run, want 600", f, st.Mode().Perm())
			}
		}
		if b, _ := os.ReadFile(token); string(b) != "secret\n" {
			t.Error("the second run changed the token")
		}
		got, err := os.ReadFile(filepath.Join(root, "usr/local/bin/inventory-agent"))
		if err != nil || !strings.Contains(string(got), "echo "+arch) {
			t.Errorf("%s: the installed binary is not the one built for %s", name, arch)
		}
	}
}

func TestInstallRefusesWithoutRoot(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a POSIX shell and a non-root user")
	}
	cmd := exec.Command("sh", "../../deploy/agent/install.sh")
	cmd.Env = append(os.Environ(), "INSTALL_ROOT=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh ran outside a staging root as a non-root user:\n%s", out)
	}
	// The refusal itself, not a later "permission denied" from a system directory.
	if !strings.Contains(string(out), "run as root") {
		t.Errorf("install.sh failed for another reason than the root check:\n%s", out)
	}
}

// Run from a source checkout there is no binary next to the script: it must stop
// before creating anything, and so must a config path that does not exist.
func TestInstallStopsBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	for name, args := range map[string][]string{
		"no binary next to the script": {"../../deploy/agent/install.sh"},
		"missing config file":          {"../../deploy/agent/install.sh", "--config", "/nonexistent/config.json"},
	} {
		root := t.TempDir()
		cmd := exec.Command("sh", args...)
		cmd.Env = append(os.Environ(), "INSTALL_ROOT="+root)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("%s: install.sh succeeded:\n%s", name, out)
		}
		if left, _ := os.ReadDir(root); len(left) != 0 {
			t.Errorf("%s: install.sh left %d entries behind", name, len(left))
		}
	}
}

// With GNU tar the same inputs and SOURCE_DATE_EPOCH must give the same archive,
// whatever the umask of the caller.
func TestAgentBundleIsReproducible(t *testing.T) {
	if out, err := exec.Command("tar", "--version").Output(); err != nil || !strings.Contains(string(out), "GNU tar") {
		t.Skip("reproducible archives need GNU tar")
	}
	const version = "v9.9.9"
	build := func(umask string) map[string]string {
		dist := t.TempDir()
		for _, arch := range []string{"amd64", "arm64"} {
			bin := filepath.Join(dist, "inventory-agent_"+version+"_linux_"+arch)
			if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+arch+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		script, err := filepath.Abs("../../deploy/agent/bundle.sh")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", "umask "+umask+" && sh \"$0\" \"$1\" \"$2\"", script, version, dist)
		cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH=1700000000")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bundle.sh: %v\n%s", err, out)
		}
		sums := map[string]string{}
		for _, arch := range []string{"amd64", "arm64"} {
			name := "inventory-agent_" + version + "_linux_" + arch + ".tar.gz"
			data, err := os.ReadFile(filepath.Join(dist, name))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			sums[name] = hex.EncodeToString(sum[:])
		}
		return sums
	}
	first, second := build("022"), build("077")
	for name, sum := range first {
		if second[name] != sum {
			t.Errorf("%s: two builds from the same inputs differ (%s, %s)", name, sum, second[name])
		}
	}
}

func runCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// extract unpacks a .tar.gz and returns the mode of every regular file in it.
func extract(t *testing.T, archive, dest string) map[string]os.FileMode {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]os.FileMode{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dest, filepath.Clean(h.Name))
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) {
			t.Fatalf("archive entry %q escapes the destination", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, h.FileInfo().Mode().Perm())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			modes[strings.TrimSuffix(h.Name, "/")] = h.FileInfo().Mode().Perm()
		default:
			t.Fatalf("archive entry %q has unexpected type %c", h.Name, h.Typeflag)
		}
	}
	return modes
}
