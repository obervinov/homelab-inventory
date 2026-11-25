package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckURL(t *testing.T) {
	ok := []string{"https://inventory.example.com/v1/report", "http://localhost:8080/v1/report", "http://127.0.0.1:8080/v1/report", "http://[::1]:8080/v1/report"}
	bad := []string{"http://inventory.example.com/v1/report", "ftp://example.com/x", "inventory.example.com/v1/report", "", "https://"}
	for _, u := range ok {
		if err := CheckURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := CheckURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestReadTrustedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if raw, err := ReadTrustedFile(p); err != nil || string(raw) != "{}" {
		t.Fatalf("0600 in 0700 rejected: %q %v", raw, err)
	}
	for _, mode := range []os.FileMode{0o660, 0o606, 0o666} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTrustedFile(p); err == nil {
			t.Errorf("file mode %04o accepted", mode)
		}
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}

	// A symlink is refused even when it points at an acceptable file.
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrustedFile(link); err == nil {
		t.Error("symlink accepted")
	}

	// Whoever can write the directory can replace the file.
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTrustedFile(p); err == nil {
			t.Errorf("directory mode %04o accepted", mode)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrustedFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := ReadTrustedFile(dir); err == nil {
		t.Error("directory accepted as a file")
	}
}
