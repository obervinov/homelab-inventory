// Package kube reads what is deployed from the in-cluster Kubernetes API:
// k3s HelmChart resources (chart + version) and the images of live pods.
// It talks plain REST with the pod's ServiceAccount token, so the service
// needs no client library and only get/list on those two resources.
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
	defer resp.Body.Close()
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

// Collect returns one item per chart release and one per distinct image:tag.
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
	sort.SliceStable(items, func(i, j int) bool { return items[i].Kind+items[i].Name < items[j].Kind+items[j].Name })
	return items, nil
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
