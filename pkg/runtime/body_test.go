package runtime

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
