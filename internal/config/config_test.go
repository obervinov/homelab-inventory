package config

import "testing"

func TestGlob(t *testing.T) {
	cases := []struct {
		p, s string
		want bool
	}{
		{"a/b", "a/b", true}, {"a/b", "a/bc", false},
		{"ghcr.io/org/*", "ghcr.io/org/x/y", true}, {"*/n8n", "docker.io/n8nio/n8n", true},
		{"*n8n*", "ghcr.io/x/n8n-mcp", true}, {"a*b*c", "a--b--c", true}, {"a*b*c", "a--c", false},
		{"*", "anything", true}, {"ab*ba", "aba", false},
	}
	for _, c := range cases {
		if got := Glob(c.p, c.s); got != c.want {
			t.Errorf("Glob(%q, %q) = %v", c.p, c.s, got)
		}
	}
}

func TestParseRejects(t *testing.T) {
	h := "0000000000000000000000000000000000000000000000000000000000000000"
	bad := []string{
		`{}`,
		`{"read_token_sha256":"` + h + `","sources":[{"name":"a","token_sha256":"short"}]}`,
		`{"read_token_sha256":"` + h + `","sources":[{"name":"a","token_sha256":"` + h + `"},{"name":"a","token_sha256":"` + h + `"}]}`,
		`{"read_token_sha256":"` + h + `","kubernetes":{"enabled":true},"sources":[{"name":"k8s","token_sha256":"` + h + `"}]}`,
		`{"read_token_sha256":"` + h + `","stale_after":"soon"}`,
		`{"read_token_sha256":"` + h + `","unknown":1}`,
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("accepted: %s", b)
		}
	}
}
