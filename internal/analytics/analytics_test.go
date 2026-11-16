package analytics

import (
	"strings"
	"testing"
)

func TestEnabledForHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{PublicHost, true},
		{PublicHost + ":443", true},
		{"sth.sun-praise.com:8443", true},
		{"192.168.2.15", false},
		{"192.168.2.15:3939", false},
		{"localhost:3939", false},
		{"evil-" + PublicHost, false},
		{PublicHost + ".evil.example", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := EnabledForHost(tc.host); got != tc.want {
			t.Errorf("EnabledForHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// TestScriptTagContract pins the tracker's reporting guard: data-domains must
// stay scoped to PublicHost, and the tag must point at the self-hosted
// Umami instance. If either drifts, non-public deployments that somehow render
// the tag would report to the wrong place.
func TestScriptTagContract(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"analysis.sun-praise.com/script.js",
		`data-website-id="`,
		`data-domains="` + PublicHost + `"`,
	} {
		if !strings.Contains(ScriptTag, want) {
			t.Errorf("ScriptTag missing %q:\n%s", want, ScriptTag)
		}
	}
}
