package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

type validateResult struct {
	Username string
}

// validateWithAuth checks the endpoint and optional success assertion declared
// by cli.yaml. A nil v is equivalent to passing --skip-validate.
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
	assertField := v.Assert != nil && v.Assert.Field != ""
	needsJSON := assertField || v.Display.UsernameField != "" || v.Display.FallbackField != ""
	if len(bytes.TrimSpace(data)) == 0 {
		if v.Assert != nil && (v.Assert.NonEmpty || assertField) {
			return validateResult{}, fmt.Errorf("validation assertion failed: response body is empty")
		}
		return validateResult{}, nil
	}
	if !needsJSON {
		return validateResult{}, nil
	}
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return validateResult{}, fmt.Errorf("decode response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return validateResult{}, fmt.Errorf("decode response: unexpected trailing data")
	}
	if assertField {
		value, ok := pluck(raw, v.Assert.Field)
		if !ok {
			return validateResult{}, fmt.Errorf("validation assertion failed: field %q is missing", v.Assert.Field)
		}
		if v.Assert.NonEmpty && !nonEmpty(value) {
			return validateResult{}, fmt.Errorf("validation assertion failed: field %q is empty", v.Assert.Field)
		}
	}
	user := pluckString(raw, v.Display.UsernameField)
	if user == "" {
		user = pluckString(raw, v.Display.FallbackField)
	}
	return validateResult{Username: user}, nil
}

func pluck(raw any, path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	cur := raw
	for _, part := range strings.Split(path, ".") {
		var ok bool
		switch value := cur.(type) {
		case map[string]any:
			cur, ok = value[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, false
			}
			cur, ok = value[index], true
		default:
			return nil, false
		}
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func pluckString(raw any, path string) string {
	value, ok := pluck(raw, path)
	if !ok {
		return ""
	}
	switch value.(type) {
	case nil, map[string]any, []any:
		return ""
	default:
		return fmt.Sprint(value)
	}
}

func nonEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case map[string]any:
		return len(value) > 0
	case []any:
		return len(value) > 0
	default:
		return true
	}
}
