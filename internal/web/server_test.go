package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/obervinov/homelab-inventory/internal/config"
	"github.com/obervinov/homelab-inventory/internal/store"
)

func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func newServer(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	cfg, err := config.Parse([]byte(`{
	  "sources":[{"name":"host-a","token_sha256":"` + hash("tok-a") + `"},{"name":"host-b","token_sha256":"` + hash("tok-b") + `"}],
	  "read_token_sha256":"` + hash("read") + `",
	  "stale_after":"1h",
	  "mappings":[{"kind":"docker","name":"example/app*","repo":"example/app"}],
	  "ignore":[{"name":"internal/*"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return &Server{Cfg: cfg, Store: store.New(), Now: func() time.Time { return now }}, &now
}

func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const report = `{"items":[{"name":"example/app","kind":"docker","version":"1.0"},{"name":"internal/x","kind":"docker","version":"2"},{"name":"linux","kind":"kernel","version":"6.8","available":"6.9"}]}`

func TestReportAuth(t *testing.T) {
	s, _ := newServer(t)
	for _, tok := range []string{"", "wrong", "read"} {
		if rec := do(s.Public(), "POST", "/v1/report", tok, report); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: got %d, want 401", tok, rec.Code)
		}
	}
	if rec := do(s.Public(), "POST", "/v1/report", "tok-a", report); rec.Code != http.StatusNoContent {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
}

func TestPublicListenerHasNoInventory(t *testing.T) {
	s, _ := newServer(t)
	if rec := do(s.Public(), "GET", "/v1/inventory", "read", ""); rec.Code != http.StatusNotFound {
		t.Errorf("public listener served /v1/inventory: %d", rec.Code)
	}
	if rec := do(s.Internal(), "POST", "/v1/report", "tok-a", report); rec.Code == http.StatusNoContent {
		t.Errorf("internal listener accepted a report")
	}
}

func TestReportValidation(t *testing.T) {
	s, _ := newServer(t)
	bad := []string{
		`{"items":[]}`,
		`{"items":[{"name":"x","kind":"nope","version":"1"}]}`,
		`{"items":[{"name":"","kind":"docker","version":"1"}]}`,
		`{"items":[{"name":"x","kind":"docker","version":"1"}],"source":"host-b"}`,
		`{"items":[{"name":"` + strings.Repeat("a", 300) + `","kind":"docker","version":"1"}]}`,
		`not json`,
	}
	for _, b := range bad {
		if rec := do(s.Public(), "POST", "/v1/report", "tok-a", b); rec.Code != http.StatusBadRequest {
			t.Errorf("body %.40q: got %d, want 400", b, rec.Code)
		}
	}
	big := `{"items":[{"name":"x","kind":"docker","version":"` + strings.Repeat("a", maxBody) + `"}]}`
	if rec := do(s.Public(), "POST", "/v1/report", "tok-a", big); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body: got %d", rec.Code)
	}
}

func TestInventory(t *testing.T) {
	s, now := newServer(t)
	do(s.Public(), "POST", "/v1/report", "tok-a", report)
	if rec := do(s.Internal(), "GET", "/v1/inventory", "tok-a", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("report token must not read inventory: %d", rec.Code)
	}
	*now = now.Add(2 * time.Hour)
	do(s.Public(), "POST", "/v1/report", "tok-b", `{"items":[{"name":"other","kind":"package","version":"3"}]}`)
	rec := do(s.Internal(), "GET", "/v1/inventory", "read", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var out inventoryOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	stale := map[string]bool{}
	for _, src := range out.Sources {
		stale[src.Name] = src.Stale
	}
	if !stale["host-a"] || stale["host-b"] {
		t.Errorf("staleness: %+v", out.Sources)
	}
	status := map[string]string{}
	for _, it := range out.Items {
		status[it.Source+"/"+it.Name] = it.Status + ":" + it.Repo
	}
	want := map[string]string{"host-a/example/app": "mapped:example/app", "host-a/internal/x": "ignored:", "host-a/linux": "unmapped:", "host-b/other": "unmapped:"}
	for k, v := range want {
		if status[k] != v {
			t.Errorf("%s: got %q, want %q", k, status[k], v)
		}
	}
}

func TestReportReplacesAndIsolatesSources(t *testing.T) {
	s, _ := newServer(t)
	do(s.Public(), "POST", "/v1/report", "tok-a", report)
	do(s.Public(), "POST", "/v1/report", "tok-b", `{"items":[{"name":"other","kind":"package","version":"3"}]}`)
	do(s.Public(), "POST", "/v1/report", "tok-a", `{"items":[{"name":"only","kind":"docker","version":"9"}]}`)
	a, _, _ := s.Store.Get("host-a")
	b, _, _ := s.Store.Get("host-b")
	if len(a) != 1 || a[0].Name != "only" || len(b) != 1 || b[0].Name != "other" {
		t.Errorf("a=%+v b=%+v", a, b)
	}
}

func TestNeverSeenSourceIsStale(t *testing.T) {
	s, _ := newServer(t)
	var out inventoryOut
	if err := json.Unmarshal(do(s.Internal(), "GET", "/v1/inventory", "read", "").Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sources) != 2 || !out.Sources[0].Stale || out.Sources[0].LastSeen != nil {
		t.Errorf("%+v", out.Sources)
	}
}

func TestRateLimit(t *testing.T) {
	s, _ := newServer(t)
	code := 0
	for i := 0; i <= rateLimit; i++ {
		code = do(s.Public(), "POST", "/v1/report", "wrong", report).Code
	}
	if code != http.StatusTooManyRequests {
		t.Errorf("got %d after %d requests", code, rateLimit+1)
	}
}
