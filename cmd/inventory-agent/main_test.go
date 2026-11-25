package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// A redirect must never carry the token anywhere: it is reported as an error
// and the target is not contacted.
func TestSendDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	var firstHits atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		http.Redirect(w, r, target.URL+"/v1/report", http.StatusTemporaryRedirect)
	}))
	defer first.Close()

	if err := send(context.Background(), first.URL+"/v1/report", "tok", []byte(`{}`)); err == nil {
		t.Fatal("a redirect was treated as success")
	}
	if targetHits.Load() != 0 {
		t.Errorf("the redirect target was contacted %d time(s)", targetHits.Load())
	}
	if firstHits.Load() != 1 {
		t.Errorf("a redirect was retried: %d requests", firstHits.Load())
	}
}

func TestSendAcceptsNoContent(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := send(context.Background(), srv.URL, "tok", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q", auth)
	}
}
