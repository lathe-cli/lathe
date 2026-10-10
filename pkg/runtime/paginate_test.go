package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestPaginateAll_Cursor(t *testing.T) {
	pages := []map[string]any{
		{"items": []any{map[string]any{"id": "1", "amount": int64(9007199254740993)}, map[string]any{"id": "2"}}, "next_page_token": "tok2"},
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
	testutil.Require(t, err == nil, "PaginateAll: %v", err)

	var result map[string][]map[string]any
	testutil.NoError(t, json.Unmarshal(data, &result))
	testutil.Check(t, len(result["items"]) == 3, "got %d items, want 3", len(result["items"]))
	testutil.Check(t, strings.Contains(string(data), "9007199254740993"), "merged output lost integer precision: %s", data)
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
		testutil.NoError(t, json.Unmarshal(raw, &body))
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
	testutil.Require(t, err == nil, "PaginateAll: %v", err)

	var result map[string]any
	testutil.NoError(t, json.Unmarshal(data, &result))
	items := result["data"].(map[string]any)["listApps"].(map[string]any)["nodes"].([]any)
	testutil.Check(t, len(items) == 2, "got %d items, want 2", len(items))
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
	testutil.Require(t, secondVars["after"] == "cursor-2", "second request after = %#v, want cursor-2", secondVars["after"])
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
	testutil.Require(t, err == nil, "PaginateAll: %v", err)

	var result map[string]any
	testutil.NoError(t, json.Unmarshal(data, &result))
	raw, ok := getNestedPath(result, "data.sessionList.nodes")
	testutil.Require(t, ok, "merged result missing nested list path: %s", string(data))
	items, ok := raw.([]any)
	testutil.Require(t, ok && len(items) == 3, "nested items = %#v, want 3 items", raw)
	testutil.Require(t, len(paths) == 2 && strings.Contains(paths[1], "after=tok2"), "request paths = %v, want second request to carry cursor", paths)
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
	testutil.Require(t, err == nil, "PaginateAll: %v", err)

	var result map[string][]map[string]string
	testutil.NoError(t, json.Unmarshal(data, &result))
	testutil.Check(t, len(result["data"]) == 5, "got %d items, want 5", len(result["data"]))
}

func TestPaginateAll_OffsetStartsFromRequestedPage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tokenParam  string
		query       string
		limit       int
		withTotal   bool
		wantItems   []int
		wantOffsets []int
	}{
		{name: "default offset", tokenParam: "offset", limit: 2, wantItems: []int{0, 1, 2, 3, 4}, wantOffsets: []int{0, 2, 4, 5}},
		{name: "explicit zero", tokenParam: "offset", query: "offset=0", limit: 2, withTotal: true, wantItems: []int{0, 1, 2, 3, 4}, wantOffsets: []int{0, 2, 4, 5}},
		{name: "nonzero offset", tokenParam: "offset", query: "offset=2", limit: 2, wantItems: []int{2, 3, 4}, wantOffsets: []int{2, 4, 5}},
		{name: "custom parameter", tokenParam: "skip", query: "skip=2", limit: 1, withTotal: true, wantItems: []int{2, 3, 4}, wantOffsets: []int{2, 3, 4, 5}},
		{name: "encoded parameter", tokenParam: "start_at", query: "start%5Fat=%32", limit: 3, wantItems: []int{2, 3, 4}, wantOffsets: []int{2, 5}},
		{name: "empty first page", tokenParam: "offset", query: "offset=5", limit: 2, withTotal: true, wantOffsets: []int{5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allItems := []int{0, 1, 2, 3, 4}
			var offsets []int
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				offset := 0
				_, _ = fmt.Sscanf(r.URL.Query().Get(tc.tokenParam), "%d", &offset)
				mu.Lock()
				offsets = append(offsets, offset)
				mu.Unlock()
				testutil.Check(t, r.URL.Query().Get("limit") == strconv.Itoa(tc.limit), "request lost limit: %s", r.URL)
				end := min(offset+tc.limit, len(allItems))
				response := map[string]any{"items": allItems[offset:end]}
				if tc.withTotal {
					response["total"] = len(allItems)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer srv.Close()

			hint := PaginationHint{Strategy: "offset", TokenParam: tc.tokenParam, LimitParam: "limit"}
			path := "/items?limit=" + strconv.Itoa(tc.limit)
			if tc.query != "" {
				path += "&" + tc.query
			}
			data, err := PaginateAll(context.Background(), srv.URL, "GET", path, nil, ClientOptions{Timeout: 5 * time.Second}, hint, "items", 10)
			testutil.NoError(t, err)
			var result map[string][]int
			testutil.NoError(t, json.Unmarshal(data, &result))
			testutil.Check(t, slices.Equal(result["items"], tc.wantItems), "items = %v, want %v", result["items"], tc.wantItems)
			mu.Lock()
			defer mu.Unlock()
			testutil.Check(t, slices.Equal(offsets, tc.wantOffsets), "request offsets = %v, want %v", offsets, tc.wantOffsets)
		})
	}
}

func TestBuild_OffsetPaginationStartsAtFlagValue(t *testing.T) {
	for _, defaultOffset := range []string{"", "2"} {
		t.Run("default="+defaultOffset, func(t *testing.T) {
			isolateRuntime(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				offset := 0
				_, _ = fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset)
				items := []int{0, 1, 2, 3, 4}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items[offset:min(offset+2, len(items))]})
			}))
			defer srv.Close()
			root := newExecutionRoot("raw")
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(io.Discard)
			mustBuild(t, root, "demo", []CommandSpec{{
				Group: "Items", Use: "list", Method: "GET", PathTpl: "/items",
				Params: []ParamSpec{
					{Name: "offset", Flag: "offset", In: InQuery, GoType: "int64", Default: defaultOffset},
					{Name: "limit", Flag: "limit", In: InQuery, GoType: "int64", Default: "2"},
				},
				Output:   OutputHints{ListPath: "items", Pagination: &PaginationHint{Strategy: "offset", TokenParam: "offset", LimitParam: "limit"}},
				Security: &SecurityHint{Public: true},
			}})
			args := []string{"--hostname", srv.URL, "demo", "items", "list", "--all"}
			if defaultOffset == "" {
				args = append(args, "--offset", "2")
			}
			root.SetArgs(args)
			testutil.NoError(t, root.Execute())
			var result map[string][]int
			testutil.NoError(t, json.Unmarshal(out.Bytes(), &result))
			testutil.Check(t, slices.Equal(result["items"], []int{2, 3, 4}), "CLI output = %s, want items [2 3 4]", &out)
		})
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
	testutil.Require(t, err == nil, "PaginateAll: %v", err)

	var result map[string][]map[string]string
	testutil.NoError(t, json.Unmarshal(data, &result))
	testutil.Check(t, len(result["items"]) == 3, "got %d items, want 3 (max-pages cap)", len(result["items"]))
}

func TestSetQueryParam(t *testing.T) {
	cases := []struct {
		base, key, val, want string
	}{
		{"/items", "page_token", "abc", "/items?page_token=abc"},
		{"/items?limit=10", "page_token", "abc", "/items?limit=10&page_token=abc"},
		{"/items?page_token=old&limit=10", "page_token", "new", "/items?limit=10&page_token=new"},
		{"/x?ids=a,b&p=a/b", "page_token", "t", "/x?ids=a,b&p=a/b&page_token=t"},
		{"/x?a=1;b=2&c=3", "page_token", "t", "/x?a=1;b=2&c=3&page_token=t"},
	}
	for _, tc := range cases {
		got := setQueryParam(tc.base, tc.key, tc.val)
		testutil.Check(t, got == tc.want, "setQueryParam(%q, %q, %q) = %q, want %q", tc.base, tc.key, tc.val, got, tc.want)
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
