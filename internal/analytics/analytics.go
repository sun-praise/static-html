// Package analytics owns the single source of truth for the optional Umami
// page-view tracker embedded in first-party pages (home page and /s/ preview
// pages). Login/register pages deliberately carry no tracker: they handle
// credentials, and the same-origin execution face is not worth the analytics
// value there.
//
// Injection is gated SERVER-SIDE on the request host: only the public
// deployment (PublicHost) ever receives the script — local dev and intranet
// instances never load it at all. The data-domains attribute inside the tag is
// a second, tracker-side reporting guard (defense in depth); it is NOT a load
// gate and must not be described as one.
package analytics

import "net"

// PublicHost is the only deployment whose pages carry the tracker.
const PublicHost = "sth.sun-praise.com"

// ScriptTag is the Umami tracker element, injected verbatim into page heads.
const ScriptTag = `<script defer src="https://analysis.sun-praise.com/script.js" data-website-id="26f61cff-edaa-47b0-86c1-60a26c1b5e68" data-domains="sth.sun-praise.com"></script>`

// EnabledForHost reports whether pages served for the given request host
// should carry the tracker. Port suffixes are ignored so that direct-access
// hosts (e.g. 192.168.2.15:3939, localhost:3939) never match.
func EnabledForHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host == PublicHost
}
