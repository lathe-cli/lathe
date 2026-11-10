package sourceconfig

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestLoadBasePath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    string
		wantSet bool
	}{
		{name: "omitted"},
		{name: "null", value: "null"},
		{name: "empty", value: `""`, wantSet: true},
		{name: "root", value: `"/"`, wantSet: true},
		{name: "escaped path", value: `"/api%2Fv1/%3Fq%23f/"`, want: "/api%2Fv1/%3Fq%23f", wantSet: true},
		{name: "dot segments", value: `"/api/../v1/./"`, want: "/api/../v1/.", wantSet: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadSources(t, basePathSources(BackendSwagger, tc.value))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := cfg.Sources["demo"].BasePath
			if !tc.wantSet {
				if got != nil {
					t.Fatalf("BasePath = %q, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("BasePath = nil, want an explicit override")
			}
			if *got != tc.want {
				t.Fatalf("BasePath = %q, want %q", *got, tc.want)
			}
		})
	}
}

func TestLoadBasePathRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{
		"/{t}",
		"https://example.com/api/v1",
		"//example.com/api/v1",
		"/api?",
		"/api#",
		"/%zz",
		"/api\u0085/v1",
	} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			_, err := loadSources(t, basePathSources(BackendSwagger, strconv.Quote(value)))
			if err == nil || !strings.Contains(err.Error(), "base_path") {
				t.Fatalf("Load error = %v, want base_path rejection", err)
			}
		})
	}
}

func TestLoadBasePathBackends(t *testing.T) {
	for _, backend := range []string{BackendOpenAPI3, BackendProto} {
		t.Run(backend, func(t *testing.T) {
			_, err := loadSources(t, basePathSources(backend, `""`))
			if backend == BackendOpenAPI3 {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "base_path") {
				t.Fatalf("Load error = %v, want unsupported base_path rejection", err)
			}
		})
	}
}

func basePathSources(backend, value string) string {
	field := ""
	if value != "" {
		field = "    base_path: " + value + "\n"
	}
	block := map[string]string{
		BackendSwagger:  "    swagger:\n      files: [api.json]\n",
		BackendOpenAPI3: "    openapi3:\n      files: [api.yaml]\n",
		BackendProto:    "    proto:\n      staging: [{from: ., to: api}]\n      entries: [api/service.proto]\n",
	}[backend]
	return fmt.Sprintf("sources:\n  demo:\n    repo_url: https://example.com/repo.git\n    pinned_tag: v1.2.3\n    backend: %s\n%s%s", backend, field, block)
}
