package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestJSONBodyFromFlags(t *testing.T) {
	spec := CommandSpec{
		Method:  "PATCH",
		PathTpl: "/keys/{id}/limits",
		Params: []ParamSpec{
			{Name: "id", Flag: "id", In: InPath, GoType: "string", Required: true},
			{Name: "maxBudgetUsd", Flag: "max-budget-usd", In: InBody, GoType: "float64"},
			{Name: "budgetDuration", Flag: "budget-duration", In: InBody, GoType: "string", Enum: []string{"daily", "weekly", "monthly"}},
			{Name: "rpmLimit", Flag: "rpm-limit", In: InBody, GoType: "int64"},
			{Name: "allowedModels", Flag: "allowed-models", In: InBody, GoType: "[]string", ItemEnum: []string{"model-a", "model-b"}},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
	}
	budget := 100.0
	rpm := int64(60)
	models := []string{"model-a", "model-b"}
	duration := "monthly"
	input := OperationInput{
		Values: map[string]any{
			boundParamKey(spec.Params[0]): "key-1",
			boundParamKey(spec.Params[1]): &budget,
			boundParamKey(spec.Params[2]): &duration,
			boundParamKey(spec.Params[3]): &rpm,
			boundParamKey(spec.Params[4]): &models,
		},
		Changed: map[string]bool{
			boundParamKey(spec.Params[0]): true,
			boundParamKey(spec.Params[1]): true,
			boundParamKey(spec.Params[2]): true,
			boundParamKey(spec.Params[3]): true,
			boundParamKey(spec.Params[4]): true,
		},
	}
	_, body, _, err := resolveOperationRequest(spec, input, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := encodeRequestBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"maxBudgetUsd":   float64(100),
		"budgetDuration": "monthly",
		"rpmLimit":       float64(60),
		"allowedModels":  []any{"model-a", "model-b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	models = []string{"model-a", "unknown"}
	if _, _, _, err := resolveOperationRequest(spec, input, ClientOptions{}); err == nil {
		t.Fatal("expected invalid array item error")
	}
}

func TestJSONBodyFlagsExclusiveWithFileAndSet(t *testing.T) {
	spec := CommandSpec{
		Method:  "PATCH",
		PathTpl: "/keys/{id}",
		Params: []ParamSpec{
			{Name: "maxBudgetUsd", Flag: "max-budget-usd", In: InBody, GoType: "float64"},
		},
		RequestBody: &RequestBody{Required: true, MediaType: "application/json"},
	}
	budget := 100.0
	flagKey := boundParamKey(spec.Params[0])
	_, _, _, err := resolveOperationRequest(spec, OperationInput{
		Values:   map[string]any{flagKey: &budget},
		Changed:  map[string]bool{flagKey: true},
		HasFile:  true,
		FileBody: []byte(`{"maxBudgetUsd":1}`),
	}, ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "--file cannot be combined") {
		t.Fatalf("error = %v", err)
	}
	_, _, _, err = resolveOperationRequest(spec, OperationInput{
		Values:   map[string]any{flagKey: &budget},
		Changed:  map[string]bool{flagKey: true},
		BodySets: []string{"maxBudgetUsd=null"},
	}, ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "cannot be set by both") {
		t.Fatalf("error = %v", err)
	}
	_, body, _, err := resolveOperationRequest(spec, OperationInput{
		BodySets: []string{"expiresAt=null"},
	}, ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := encodeRequestBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["expiresAt"]; !ok || got["expiresAt"] != nil {
		t.Fatalf("got %#v", got)
	}
}

func TestBuildBodyFromSet(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want map[string]any
	}{
		{
			name: "nested objects + type inference",
			in:   []string{"spec.replicas=3", "metadata.name=demo", "spec.enabled=true", "spec.weight=0.5", "spec.note=hello"},
			want: map[string]any{
				"spec": map[string]any{
					"replicas": float64(3),
					"enabled":  true,
					"weight":   0.5,
					"note":     "hello",
				},
				"metadata": map[string]any{"name": "demo"},
			},
		},
		{
			name: "null value",
			in:   []string{"a=null"},
			want: map[string]any{"a": nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := BuildBodyFromSet(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBuildEnvelopeBody(t *testing.T) {
	const tmpl = `{"query":"mutation($name:String!){createApp(name:$name){id}}","variables":{}}`

	t.Run("merges --set under merge path, keeps baked query", func(t *testing.T) {
		raw, err := buildEnvelopeBody(tmpl, "variables", nil, []string{"name=demo", "replicas=3"}, nil, nil, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		want := map[string]any{
			"query":     "mutation($name:String!){createApp(name:$name){id}}",
			"variables": map[string]any{"name": "demo", "replicas": float64(3)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %#v, want %#v", got, want)
		}
	})

	t.Run("merges empty array literal under merge path", func(t *testing.T) {
		raw, err := buildEnvelopeBody(tmpl, "variables", nil, []string{"input.skillIds=[]"}, nil, nil, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		vars, _ := got["variables"].(map[string]any)
		input, _ := vars["input"].(map[string]any)
		if !reflect.DeepEqual(input["skillIds"], []any{}) {
			t.Errorf("skillIds = %#v, want empty array", input["skillIds"])
		}
	})

	t.Run("merges typed variable values at merge path", func(t *testing.T) {
		raw, err := buildEnvelopeBody(tmpl, "variables", map[string]any{"name": "demo", "count": int64(3)}, nil, nil, nil, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		vars, _ := got["variables"].(map[string]any)
		if vars["name"] != "demo" || vars["count"] != float64(3) {
			t.Errorf("variables = %#v, want name=demo count=3", vars)
		}
	})

	t.Run("no user input sends template unchanged", func(t *testing.T) {
		raw, err := buildEnvelopeBody(tmpl, "variables", nil, nil, nil, nil, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if !reflect.DeepEqual(got["variables"], map[string]any{}) {
			t.Errorf("variables = %#v, want empty object", got["variables"])
		}
	})

	t.Run("--file replaces merge target", func(t *testing.T) {
		raw, err := buildEnvelopeBody(tmpl, "variables", nil, nil, nil, []byte(`{"name":"from-file"}`), true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if !reflect.DeepEqual(got["variables"], map[string]any{"name": "from-file"}) {
			t.Errorf("variables = %#v", got["variables"])
		}
	})

	t.Run("--file with empty merge path is rejected", func(t *testing.T) {
		if _, err := buildEnvelopeBody(tmpl, "", nil, nil, nil, []byte(`{}`), true); err == nil {
			t.Fatal("expected error for --file with empty merge path")
		}
	})

	t.Run("invalid template is reported", func(t *testing.T) {
		if _, err := buildEnvelopeBody(`{not json`, "variables", nil, nil, nil, nil, false); err == nil {
			t.Fatal("expected error for invalid template")
		}
	})
}

func TestBuildBodyFromSet_SetStrKeepsStrings(t *testing.T) {
	raw, err := buildBodyFromSet(
		[]string{"spec.replicas=3", "spec.enabled=true"},
		[]string{"spec.stringReplicas=3", "spec.stringEnabled=true", "metadata.name=demo"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := map[string]any{
		"spec": map[string]any{
			"replicas":       float64(3),
			"enabled":        true,
			"stringReplicas": "3",
			"stringEnabled":  "true",
		},
		"metadata": map[string]any{"name": "demo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestBuildBodyFromSet_ArrayIndex(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want map[string]any
	}{
		{
			name: "simple array",
			in:   []string{"items[0]=a", "items[1]=b"},
			want: map[string]any{"items": []any{"a", "b"}},
		},
		{
			name: "empty array literal",
			in:   []string{"items=[]"},
			want: map[string]any{"items": []any{}},
		},
		{
			name: "array with type inference",
			in:   []string{"ids[0]=1", "ids[1]=2"},
			want: map[string]any{"ids": []any{float64(1), float64(2)}},
		},
		{
			name: "array of objects",
			in:   []string{"containers[0].name=nginx", "containers[0].image=nginx:latest", "containers[1].name=sidecar"},
			want: map[string]any{
				"containers": []any{
					map[string]any{"name": "nginx", "image": "nginx:latest"},
					map[string]any{"name": "sidecar"},
				},
			},
		},
		{
			name: "nested array under object",
			in:   []string{"spec.ports[0]=8080", "spec.ports[1]=9090"},
			want: map[string]any{
				"spec": map[string]any{
					"ports": []any{float64(8080), float64(9090)},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := BuildBodyFromSet(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestBuildBodyFromSet_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   []string
	}{
		{"missing equals", []string{"foo"}},
		{"empty key", []string{"=value"}},
		{"path conflict", []string{"a=1", "a.b=2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildBodyFromSet(tc.in); err == nil {
				t.Errorf("expected error for %v", tc.in)
			}
		})
	}
}

func TestReadStdin(t *testing.T) {
	errRead := errors.New("read failed")
	cases := []struct {
		name     string
		reader   func(t *testing.T) io.Reader
		ctx      func(t *testing.T) context.Context
		wantData string
		wantErr  error
	}{
		{
			name:     "reads until EOF",
			reader:   func(*testing.T) io.Reader { return strings.NewReader(`{"name":"demo"}`) },
			ctx:      func(*testing.T) context.Context { return context.Background() },
			wantData: `{"name":"demo"}`,
		},
		{
			name:    "propagates read errors",
			reader:  func(*testing.T) io.Reader { return iotest.ErrReader(errRead) },
			ctx:     func(*testing.T) context.Context { return context.Background() },
			wantErr: errRead,
		},
		{
			name:   "already canceled context skips the read",
			reader: func(*testing.T) io.Reader { return iotest.ErrReader(errRead) },
			ctx: func(*testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr: context.Canceled,
		},
		{
			name:   "cancellation interrupts a blocked read",
			reader: func(t *testing.T) io.Reader { return blockedReader(t) },
			ctx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				time.AfterFunc(50*time.Millisecond, cancel)
				return ctx
			},
			wantErr: context.Canceled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := readStdinWithin(t, tc.ctx(t), tc.reader(t), 5*time.Second)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if string(data) != tc.wantData {
				t.Fatalf("data = %q, want %q", data, tc.wantData)
			}
		})
	}
}

func TestReadBodyContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(`{"id":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := ReadBodyContext(context.Background(), path)
	if err != nil || string(data) != `{"id":1}` {
		t.Fatalf("ReadBodyContext(file) = %q, %v", data, err)
	}
	data, err = ReadBody(path)
	if err != nil || string(data) != `{"id":1}` {
		t.Fatalf("ReadBody(file) = %q, %v", data, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadBodyContext(ctx, "-"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadBodyContext(canceled, \"-\") err = %v, want context.Canceled", err)
	}
}

// blockedReader returns the read end of a pipe that never receives data, so
// reads block until the test finishes and the write end is closed.
func blockedReader(t *testing.T) io.Reader {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	return pr
}

// readStdinWithin runs readStdin and fails the test if it does not return
// within timeout, so a regression to a blocking read cannot hang the suite.
func readStdinWithin(t *testing.T, ctx context.Context, r io.Reader, timeout time.Duration) ([]byte, error) {
	t.Helper()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := readStdin(ctx, r)
		done <- result{data: data, err: err}
	}()
	select {
	case res := <-done:
		return res.data, res.err
	case <-time.After(timeout):
		t.Fatalf("readStdin did not return within %s", timeout)
		return nil, nil
	}
}
