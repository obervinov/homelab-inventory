// Package web serves two separate listeners so exposure is structural, not a
// matter of routing rules: the public one accepts reports and nothing else,
// the internal one serves the inventory and is never published.
package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/obervinov/homelab-inventory/internal/config"
	"github.com/obervinov/homelab-inventory/internal/store"
)

const (
	maxBody   = 256 << 10
	maxItems  = 1000
	maxField  = 256
	rateLimit = 30 // requests per minute per client on the public listener
)

var kinds = map[string]bool{"docker": true, "package": true, "kernel": true, "os": true, "binary": true}

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Now   func() time.Time
	// TrustForwardedFor: take the client address from X-Forwarded-For (set
	// when the public listener sits behind the gateway).
	TrustForwardedFor bool

	mu      sync.Mutex
	window  time.Time
	clients map[string]int
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func tokenHash(r *http.Request) []byte {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil
	}
	sum := sha256.Sum256([]byte(strings.TrimPrefix(h, "Bearer ")))
	out := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(out, sum[:])
	return out
}

// sourceFor resolves the reporting source from the token alone. Every entry
// is compared so timing does not reveal which source a token is close to.
func (s *Server) sourceFor(r *http.Request) string {
	got := tokenHash(r)
	if got == nil {
		return ""
	}
	found := ""
	for _, src := range s.Cfg.Sources {
		if subtle.ConstantTimeCompare(got, []byte(strings.ToLower(src.TokenSHA256))) == 1 {
			found = src.Name
		}
	}
	return found
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustForwardedFor {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.window) >= time.Minute || s.clients == nil {
		s.window, s.clients = now, map[string]int{}
	}
	s.clients[ip]++
	return s.clients[ip] <= rateLimit
}

// Public is the internet-facing handler: POST /v1/report and a health probe.
func (s *Server) Public() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/report", s.report)
	return mux
}

// Internal is the cluster-only handler: GET /v1/inventory.
func (s *Server) Internal() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /v1/inventory", s.inventory)
	return mux
}

func health(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) }

type reportBody struct {
	Items []store.Item `json:"items"`
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if !s.allow(s.clientIP(r)) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	source := s.sourceFor(r)
	if source == "" {
		// One answer for a missing, malformed or unknown token.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body reportBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body.Items) == 0 || len(body.Items) > maxItems {
		http.Error(w, "bad request: items must hold 1.."+"1000 entries", http.StatusBadRequest)
		return
	}
	for _, it := range body.Items {
		if it.Name == "" || it.Version == "" || !kinds[it.Kind] ||
			len(it.Name) > maxField || len(it.Version) > maxField || len(it.Available) > maxField || len(it.Ref) > maxField {
			http.Error(w, "bad request: invalid item", http.StatusBadRequest)
			return
		}
	}
	s.Store.Replace(source, body.Items, s.now())
	w.WriteHeader(http.StatusNoContent)
}

type outItem struct {
	Source string `json:"source"`
	store.Item
	Repo   string `json:"repo,omitempty"`
	Status string `json:"status"`
}

type outSource struct {
	Name     string     `json:"name"`
	LastSeen *time.Time `json:"last_seen"`
	Stale    bool       `json:"stale"`
	Items    int        `json:"items"`
}

type inventoryOut struct {
	GeneratedAt time.Time   `json:"generated_at"`
	Sources     []outSource `json:"sources"`
	Items       []outItem   `json:"items"`
}

func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	got := tokenHash(r)
	if got == nil || subtle.ConstantTimeCompare(got, []byte(strings.ToLower(s.Cfg.ReadTokenSHA256))) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	now := s.now()
	names := []string{}
	if s.Cfg.Kubernetes.Enabled {
		names = append(names, s.Cfg.Kubernetes.Source)
	}
	for _, src := range s.Cfg.Sources {
		names = append(names, src.Name)
	}
	sort.Strings(names)
	out := inventoryOut{GeneratedAt: now.UTC(), Sources: []outSource{}, Items: []outItem{}}
	for _, name := range names {
		items, seen, ok := s.Store.Get(name)
		src := outSource{Name: name, Stale: true, Items: len(items)}
		if ok {
			t := seen.UTC()
			src.LastSeen = &t
			src.Stale = now.Sub(seen) > s.Cfg.StaleAfterDuration()
		}
		out.Sources = append(out.Sources, src)
		for _, it := range items {
			repo, status := s.Cfg.Classify(it.Kind, it.Name)
			out.Items = append(out.Items, outItem{Source: name, Item: it, Repo: repo, Status: status})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
