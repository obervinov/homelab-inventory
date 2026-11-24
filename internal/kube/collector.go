// Package kube reads what is deployed from the in-cluster Kubernetes API:
// k3s HelmChart resources (chart + version), the images of live pods, and the
// kubelet and kernel versions of the nodes.
// It talks plain REST with the pod's ServiceAccount token, so the service
// needs no client library and only get/list on helmcharts, pods and nodes.
package kube

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/obervinov/homelab-inventory/internal/store"
)

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"

type Collector struct {
	BaseURL   string
	TokenPath string
	Client    *http.Client
	// NodesErr is the error of the last attempt to list nodes, nil when it worked.
	NodesErr error
}

// InCluster builds a collector from the mounted ServiceAccount.
func InCluster() (*Collector, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("kube: not running in a cluster")
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("kube: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("kube: no CA certificates in ca.crt")
	}
	return &Collector{
		BaseURL:   "https://" + host + ":" + port,
		TokenPath: saDir + "/token",
		Client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
	}, nil
}

func (c *Collector) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.TokenPath != "" {
		// Re-read on every call: projected tokens rotate.
		tok, err := os.ReadFile(c.TokenPath)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("kube: GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type helmCharts struct {
	Items []struct {
		Metadata struct{ Namespace, Name string } `json:"metadata"`
		Spec     struct {
			Chart   string `json:"chart"`
			Version string `json:"version"`
		} `json:"spec"`
	} `json:"items"`
}

type pods struct {
	Items []struct {
		Metadata struct{ Namespace, Name string } `json:"metadata"`
		Spec     struct {
			Containers     []struct{ Image string } `json:"containers"`
			InitContainers []struct{ Image string } `json:"initContainers"`
		} `json:"spec"`
		Status struct{ Phase string } `json:"status"`
	} `json:"items"`
}

type nodes struct {
	Items []struct {
		Metadata struct{ Name string } `json:"metadata"`
		Status   struct {
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
				KernelVersion  string `json:"kernelVersion"`
			} `json:"nodeInfo"`
		} `json:"status"`
	} `json:"items"`
}

// Collect returns one item per chart release, one per distinct image:tag and,
// when nodes are readable, one per distinct kubelet and kernel version.
func (c *Collector) Collect(ctx context.Context) ([]store.Item, error) {
	var hc helmCharts
	if err := c.get(ctx, "/apis/helm.cattle.io/v1/helmcharts", &hc); err != nil {
		return nil, err
	}
	var pl pods
	if err := c.get(ctx, "/api/v1/pods", &pl); err != nil {
		return nil, err
	}
	var items []store.Item
	for _, h := range hc.Items {
		items = append(items, store.Item{
			Kind:    "chart",
			Name:    ChartName(h.Spec.Chart),
			Version: strings.TrimPrefix(h.Spec.Version, "v"),
			Ref:     h.Metadata.Namespace + "/" + h.Metadata.Name,
		})
	}
	seen := map[string]bool{}
	for _, p := range pl.Items {
		// Finished pods (helm-install jobs, cron runs) say nothing about what runs now.
		if p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed" {
			continue
		}
		images := append(p.Spec.Containers, p.Spec.InitContainers...)
		for _, ctr := range images {
			name, version := ParseImage(ctr.Image)
			key := name + "\x00" + version
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, store.Item{Kind: "image", Name: name, Version: version, Ref: p.Metadata.Namespace + "/" + p.Metadata.Name})
		}
	}
	// Nodes are optional: a role written for an older release has no access to
	// them, and that must not cost the charts and images collected above.
	var nl nodes
	if err := c.get(ctx, "/api/v1/nodes", &nl); err != nil {
		c.NodesErr = err
	} else {
		c.NodesErr = nil
		for _, n := range nl.Items {
			info := n.Status.NodeInfo
			for _, it := range []store.Item{
				{Kind: "kubelet", Name: Distribution(info.KubeletVersion), Version: strings.TrimPrefix(info.KubeletVersion, "v"), Ref: n.Metadata.Name},
				{Kind: "kernel", Name: "linux", Version: info.KernelVersion, Ref: n.Metadata.Name},
			} {
				key := it.Kind + "\x00" + it.Name + "\x00" + it.Version
				if it.Version == "" || seen[key] {
					continue
				}
				seen[key] = true
				items = append(items, it)
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Kind+items[i].Name < items[j].Kind+items[j].Name })
	return items, nil
}

// Distribution names the Kubernetes flavour from a kubelet version:
// "v1.31.4+k3s1" → "k3s", "v1.31.4+rke2r1" → "rke2", anything else "kubernetes".
func Distribution(kubeletVersion string) string {
	_, build, ok := strings.Cut(kubeletVersion, "+")
	switch {
	case ok && strings.HasPrefix(build, "k3s"):
		return "k3s"
	case ok && strings.HasPrefix(build, "rke2"):
		return "rke2"
	}
	return "kubernetes"
}

// ChartName reduces "oci://ghcr.io/org/charts/app" to "app".
func ChartName(chart string) string {
	chart = strings.TrimSuffix(chart, "/")
	if i := strings.LastIndex(chart, "/"); i >= 0 {
		return chart[i+1:]
	}
	return chart
}

// ParseImage splits an image reference into repository and version.
// "ghcr.io/a/b:1.2@sha256:…" → ("ghcr.io/a/b", "1.2"); a digest-only
// reference keeps a short digest as its version; no tag means "latest".
func ParseImage(ref string) (name, version string) {
	ref = strings.TrimSpace(ref)
	digest := ""
	if i := strings.Index(ref, "@"); i >= 0 {
		digest = ref[i+1:]
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	if digest != "" {
		if len(digest) > 19 {
			digest = digest[:19]
		}
		return ref, "@" + digest
	}
	return ref, "latest"
}
