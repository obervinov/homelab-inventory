package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// deploy/kubernetes.yaml is what people apply, so it must point at the release
// being cut and grant exactly what the collector reads.
func TestDeployManifestMatchesRelease(t *testing.T) {
	manifest, err := os.ReadFile("../../deploy/kubernetes.yaml")
	if err != nil {
		t.Fatal(err)
	}
	changelog, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+) `).FindSubmatch(changelog)
	if m == nil {
		t.Fatal("no version heading in CHANGELOG.md")
	}
	want := "image: ghcr.io/obervinov/homelab-inventory:" + string(m[1])
	if !strings.Contains(string(manifest), want) {
		t.Errorf("deploy/kubernetes.yaml does not contain %q", want)
	}
	for _, resource := range []string{`"pods"`, `"nodes"`, `"helmcharts"`} {
		if !strings.Contains(string(manifest), resource) {
			t.Errorf("ClusterRole in deploy/kubernetes.yaml lacks %s", resource)
		}
	}
}
