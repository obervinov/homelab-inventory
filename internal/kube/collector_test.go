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
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"namespace":"sys","name":"cm"},"spec":{"chart":"cert-manager","version":"v1.2.3"}}]}`))
		case "/api/v1/pods":
			_, _ = w.Write([]byte(`{"items":[
			 {"metadata":{"namespace":"a","name":"p1"},"spec":{"containers":[{"image":"x/app:1.0"},{"image":"x/side:2"}]},"status":{"phase":"Running"}},
			 {"metadata":{"namespace":"a","name":"p2"},"spec":{"containers":[{"image":"x/app:1.0"}]},"status":{"phase":"Running"}},
			 {"metadata":{"namespace":"a","name":"job"},"spec":{"containers":[{"image":"x/old:0.1"}]},"status":{"phase":"Succeeded"}}]}`))
		case "/api/v1/nodes":
			_, _ = w.Write([]byte(`{"items":[
			 {"metadata":{"name":"n1"},"status":{"nodeInfo":{"kubeletVersion":"v1.31.4+k3s1","kernelVersion":"6.8.0-1"}}},
			 {"metadata":{"name":"n2"},"status":{"nodeInfo":{"kubeletVersion":"v1.31.4+k3s1","kernelVersion":"6.12.0-2"}}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	items, err := (&Collector{BaseURL: api.URL, Client: api.Client()}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 6 {
		t.Fatalf("want chart + 2 images + 1 kubelet + 2 kernels, got %d: %+v", len(items), items)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Kind+"/"+it.Name+"/"+it.Version] = it.Ref
	}
	if got["kubelet/k3s/1.31.4+k3s1"] != "n1" || got["kernel/linux/6.8.0-1"] != "n1" || got["kernel/linux/6.12.0-2"] != "n2" {
		t.Errorf("node items: %+v", got)
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

// A role without access to nodes must still yield charts and images.
func TestCollectWithoutNodeAccess(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/helm.cattle.io/v1/helmcharts":
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"namespace":"sys","name":"cm"},"spec":{"chart":"cert-manager","version":"v1.2.3"}}]}`))
		case "/api/v1/pods":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer api.Close()
	c := &Collector{BaseURL: api.URL, Client: api.Client()}
	items, err := c.Collect(context.Background())
	if err != nil || len(items) != 1 || c.NodesErr == nil {
		t.Fatalf("items=%+v err=%v nodesErr=%v", items, err, c.NodesErr)
	}
}

func TestDistribution(t *testing.T) {
	for in, want := range map[string]string{"v1.31.4+k3s1": "k3s", "v1.31.4+rke2r1": "rke2", "v1.31.4": "kubernetes", "": "kubernetes"} {
		if got := Distribution(in); got != want {
			t.Errorf("Distribution(%q) = %q, want %q", in, got, want)
		}
	}
}
