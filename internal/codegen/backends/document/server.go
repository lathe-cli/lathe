package document

import (
	"fmt"
	"net/url"
	"strings"
)

func ServerBasePath(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, "{}") || !u.IsAbs() && !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("unsupported server URL")
	}
	return strings.TrimRight(u.EscapedPath(), "/"), nil
}
