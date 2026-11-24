package kube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseImage(t *testing.T) {
	cases := []struct{ in, name, version string }{
		{"ghcr.io/a/b:1.2.3", "ghcr.io/a/b", "1.2.3"},
		{"ghcr.io/a/b:1.2@sha256:0123456789abcdef0123456789abcdef", "ghcr.io/a/b", "1.2"},
		{"registry.local:5000/a/b", "registry.local:5000/a/b", "latest"},
		{"registry.local:5000/a/b:v2", "registry.local:5000/a/b", "v2"},
		{"a/b@sha256:0123456789abcdef0123456789abcdef", "a/b", "@sha256:0123456789ab"},
		{"nginx", "nginx", "latest"},
	}
	for _, c := range cases {
		n, v := ParseImage(c.in)
		if n != c.name || v != c.version {
			t.Errorf("ParseImage(%q) = (%q, %q), want (%q, %q)", c.in, n, v, c.name, c.version)
		}
	}
}

func TestChartName(t *testing.T) {
	if got := ChartName("oci://ghcr.io/org/helm-charts/app"); got != "app" {
		t.Errorf("got %q", got)
	}
	if got := ChartName("cert-manager"); got != "cert-manager" {
		t.Errorf("got %q", got)
	}
}

func TestCollect(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/helm.cattle.io/v1/helmcharts":
			w.Write([]byte(`{"items":[{"metadata":{"namespace":"sys","name":"cm"},"spec":{"chart":"cert-manager","version":"v1.2.3"}}]}`))
		case "/api/v1/pods":
			w.Write([]byte(`{"items":[
			 {"metadata":{"namespace":"a","name":"p1"},"spec":{"containers":[{"image":"x/app:1.0"},{"image":"x/side:2"}]},"status":{"phase":"Running"}},
			 {"metadata":{"namespace":"a","name":"p2"},"spec":{"containers":[{"image":"x/app:1.0"}]},"status":{"phase":"Running"}},
			 {"metadata":{"namespace":"a","name":"job"},"spec":{"containers":[{"image":"x/old:0.1"}]},"status":{"phase":"Succeeded"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	items, err := (&Collector{BaseURL: api.URL, Client: api.Client()}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want chart + 2 distinct images, got %d: %+v", len(items), items)
	}
	if items[0].Kind != "chart" || items[0].Name != "cert-manager" || items[0].Version != "1.2.3" {
		t.Errorf("chart item: %+v", items[0])
	}
	for _, it := range items {
		if it.Name == "x/old" {
			t.Errorf("finished pod must not be reported: %+v", it)
		}
	}
}
