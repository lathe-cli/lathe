package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

type validateResult struct {
	Username string
}

// validateToken hits the auth validation endpoint declared by cli.yaml and
// plucks a display username from the JSON response. A nil v means the
// manifest has no auth.validate block, which is equivalent to passing
// --skip-validate: returns a zero result with nil error.
func validateWithAuth(ctx context.Context, hostname string, auth runtime.Authenticator, v *config.AuthValidate, opts runtime.ClientOptions) (validateResult, error) {
	if v == nil {
		return validateResult{}, nil
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	method := v.Method
	if method == "" {
		method = "GET"
	}
	opts.Auth = auth
	data, err := runtime.DoRaw(ctx, hostname, method, v.Path, nil, opts)
	if err != nil {
		return validateResult{}, err
	}
	if len(data) == 0 {
		return validateResult{}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return validateResult{}, fmt.Errorf("decode response: %w", err)
	}
	user := pluck(raw, v.Display.UsernameField)
	if user == "" {
		user = pluck(raw, v.Display.FallbackField)
	}
	return validateResult{Username: user}, nil
}

// pluck walks a dot-separated path (e.g. "data.user.name") through a decoded
// JSON object and returns the final value as a string. A plain key
// (e.g. "username") walks a single step. Returns "" if any segment is
// missing or if the leaf is not a string.
func pluck(raw map[string]any, path string) string {
	if path == "" {
		return ""
	}
	var cur any = raw
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[p]
		if !ok {
			return ""
		}
	}
	s, ok := cur.(string)
	if !ok {
		return ""
	}
	return s
}
