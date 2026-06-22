package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPaginateAll_Cursor(t *testing.T) {
	pages := []map[string]any{
		{"items": []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}, "next_page_token": "tok2"},
		{"items": []any{map[string]any{"id": "3"}}, "next_page_token": ""},
	}
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.LoadInt32(&call))
		if idx >= len(pages) {
			idx = len(pages) - 1
		}
		atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pages[idx])
	}))
	defer srv.Close()

	hint := PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token", LimitParam: "limit"}
	data, err := PaginateAll(context.Background(), srv.URL, "GET", "/items?limit=2", nil, ClientOptions{Timeout: 5 * time.Second}, hint, "items", 10)
	if err != nil {
		t.Fatalf("PaginateAll: %v", err)
	}

	var result map[string][]map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result["items"]) != 3 {
		t.Errorf("got %d items, want 3", len(result["items"]))
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Errorf("made %d requests, want 2", atomic.LoadInt32(&call))
	}
}

func TestPaginateAll_BodyCursor(t *testing.T) {
	pages := []map[string]any{
		{
			"data": map[string]any{
				"listApps": map[string]any{
					"nodes": []any{map[string]any{"id": "1"}},
					"pageInfo": map[string]any{
						"endCursor":   "cursor-2",
						"hasNextPage": true,
					},
				},
			},
		},
		{
			"data": map[string]any{
				"listApps": map[string]any{
					"nodes": []any{map[string]any{"id": "2"}},
					"pageInfo": map[string]any{
						"endCursor":   "cursor-3",
						"hasNextPage": false,
					},
				},
			},
		},
	}
	var bodies []map[string]any
	var bodiesMu sync.Mutex
	var call int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		bodiesMu.Lock()
		bodies = append(bodies, body)
		bodiesMu.Unlock()
		idx := int(atomic.LoadInt32(&call))
		if idx >= len(pages) {
			idx = len(pages) - 1
		}
		atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pages[idx])
	}))
	defer srv.Close()

	body := []byte(`{"query":"query listApps($first: Int, $after: String) { listApps(first: $first, after: $after) { nodes { id } pageInfo { endCursor hasNextPage } } }","variables":{"first":1}}`)
	hint := PaginationHint{Strategy: "body-cursor", TokenParam: "variables.after", TokenField: "data.listApps.pageInfo.endCursor", LimitParam: "variables.first"}
	data, err := PaginateAll(context.Background(), srv.URL, "POST", "/graphql", body, ClientOptions{Timeout: 5 * time.Second}, hint, "data.listApps.nodes", 10)
	if err != nil {
		t.Fatalf("PaginateAll: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	items := result["data"].(map[string]any)["listApps"].(map[string]any)["nodes"].([]any)
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
	if atomic.LoadInt32(&call) != 2 {
		t.Errorf("made %d requests, want 2", atomic.LoadInt32(&call))
	}
	bodiesMu.Lock()
	defer bodiesMu.Unlock()
	firstVars := bodies[0]["variables"].(map[string]any)
	if _, ok := firstVars["after"]; ok {
		t.Fatalf("first request after = %#v, want absent", firstVars["after"])
	}
	secondVars := bodies[1]["variables"].(map[string]any)
	if secondVars["after"] != "cursor-2" {
		t.Fatalf("second request after = %#v, want cursor-2", secondVars["after"])
	}
}

func TestPaginateAll_CursorNestedPaths(t *testing.T) {
	pages := []map[string]any{
		{"data": map[string]any{"sessionList": map[string]any{
			"nodes":    []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}},
			"pageInfo": map[string]any{"endCursor": "tok2"},
		}}},
		{"data": map[string]any{"sessionList": map[string]any{
			"nodes":    []any{map[string]any{"id": "3"}},
			"pageInfo": map[string]any{"endCursor": ""},
		}}},
	}
	var call int32
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		idx := int(atomic.LoadInt32(&call))
		if idx >= len(pages) {
			idx = len(pages) - 1
		}
		atomic.AddInt32(&call, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pages[idx])
	}))
	defer srv.Close()

	hint := PaginationHint{Strategy: "cursor", TokenParam: "after", TokenField: "data.sessionList.pageInfo.endCursor"}
	data, err := PaginateAll(context.Background(), srv.URL, "GET", "/sessions?first=2", nil, ClientOptions{Timeout: 5 * time.Second}, hint, "data.sessionList.nodes", 10)
	if err != nil {
		t.Fatalf("PaginateAll: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, ok := getNestedPath(result, "data.sessionList.nodes")
	if !ok {
		t.Fatalf("merged result missing nested list path: %s", string(data))
	}
	items, ok := raw.([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("nested items = %#v, want 3 items", raw)
	}
	if len(paths) != 2 || !strings.Contains(paths[1], "after=tok2") {
		t.Fatalf("request paths = %v, want second request to carry cursor", paths)
	}
}

func TestPaginateAll_Offset(t *testing.T) {
	allItems := []map[string]any{{"id": "1"}, {"id": "2"}, {"id": "3"}, {"id": "4"}, {"id": "5"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off := 0
		if v := r.URL.Query().Get("offset"); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &off)
		}
		end := off + 2
		if end > len(allItems) {
			end = len(allItems)
		}
		var page []map[string]any
		if off < len(allItems) {
			page = allItems[off:end]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": page})
	}))
	defer srv.Close()

	hint := PaginationHint{Strategy: "offset", TokenParam: "offset", LimitParam: "limit"}
	data, err := PaginateAll(context.Background(), srv.URL, "GET", "/items?limit=2", nil, ClientOptions{Timeout: 5 * time.Second}, hint, "data", 10)
	if err != nil {
		t.Fatalf("PaginateAll: %v", err)
	}

	var result map[string][]map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result["data"]) != 5 {
		t.Errorf("got %d items, want 5", len(result["data"]))
	}
}

func TestPaginateAll_MaxPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":           []any{map[string]any{"id": "x"}},
			"next_page_token": "always",
		})
	}))
	defer srv.Close()

	hint := PaginationHint{Strategy: "cursor", TokenParam: "page_token", TokenField: "next_page_token"}
	data, err := PaginateAll(context.Background(), srv.URL, "GET", "/items", nil, ClientOptions{Timeout: 5 * time.Second}, hint, "items", 3)
	if err != nil {
		t.Fatalf("PaginateAll: %v", err)
	}

	var result map[string][]map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result["items"]) != 3 {
		t.Errorf("got %d items, want 3 (max-pages cap)", len(result["items"]))
	}
}

func TestSetQueryParam(t *testing.T) {
	cases := []struct {
		base, key, val, want string
	}{
		{"/items", "page_token", "abc", "/items?page_token=abc"},
		{"/items?limit=10", "page_token", "abc", "/items?limit=10&page_token=abc"},
		{"/items?page_token=old&limit=10", "page_token", "new", "/items?limit=10&page_token=new"},
	}
	for _, tc := range cases {
		got := setQueryParam(tc.base, tc.key, tc.val)
		if got != tc.want {
			t.Errorf("setQueryParam(%q, %q, %q) = %q, want %q", tc.base, tc.key, tc.val, got, tc.want)
		}
	}
}

func TestExtractJSONString(t *testing.T) {
	data := []byte(`{"next_page_token": "abc123", "count": 42, "data": {"pageInfo": {"endCursor": "nested"}}}`)
	if got := extractJSONString(data, "next_page_token"); got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
	if got := extractJSONString(data, "data.pageInfo.endCursor"); got != "nested" {
		t.Errorf("got %q, want nested", got)
	}
	if got := extractJSONString(data, "missing"); got != "" {
		t.Errorf("got %q for missing field, want empty", got)
	}
	if got := extractJSONString(data, "count"); got != "" {
		t.Errorf("got %q for non-string field, want empty", got)
	}
}
