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

func TestCheckConfigFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckConfigFile(p); err != nil {
		t.Errorf("0600 rejected: %v", err)
	}
	for _, mode := range []os.FileMode{0o660, 0o606, 0o666} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := CheckConfigFile(p); err == nil {
			t.Errorf("mode %04o accepted", mode)
		}
	}
	if err := CheckConfigFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
}
