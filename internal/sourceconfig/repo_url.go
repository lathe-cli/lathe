package sourceconfig

import (
	"net/url"
	"strings"
	"unicode"
)

func PublicRepoURL(raw string) string {
	if strings.ContainsFunc(raw, func(r rune) bool {
		return r == '`' || unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git":
			if u.User != nil && strings.EqualFold(u.Scheme, "ssh") {
				u.User = url.User(u.User.Username())
			} else {
				u.User = nil
			}
			u.RawQuery = ""
			u.ForceQuery = false
			u.Fragment = ""
			return u.String()
		}
	}
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, "?#") {
		return ""
	}
	colon := strings.Index(raw, ":")
	if colon < 0 || strings.Contains(raw[:colon], "/") {
		return ""
	}
	at := strings.LastIndex(raw[:colon], "@")
	host, path := raw[at+1:colon], raw[colon+1:]
	if len(host) < 2 || path == "" {
		return ""
	}
	return raw
}
