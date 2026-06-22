package runtime

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFormatOutput_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := FormatOutput([]byte(`{"name":"alice"}`), "json", &buf, OutputHints{})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"name\": \"alice\"\n}\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestFormatOutput_YAML(t *testing.T) {
	var buf bytes.Buffer
	err := FormatOutput([]byte(`{"name":"alice"}`), "yaml", &buf, OutputHints{})
	if err != nil {
		t.Fatal(err)
	}
	want := "name: alice\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestFormatOutput_Raw(t *testing.T) {
	var buf bytes.Buffer
	err := FormatOutput([]byte("hello"), "raw", &buf, OutputHints{})
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Errorf("got %q, want hello", buf.String())
	}
}

func TestFormatOutput_EmptyData(t *testing.T) {
	err := FormatOutput(nil, "json", io.Discard, OutputHints{})
	if err != nil {
		t.Fatalf("empty data should not error: %v", err)
	}
}

func TestFormatOutput_UnknownFormat(t *testing.T) {
	err := FormatOutput([]byte("x"), "csv", io.Discard, OutputHints{})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestFormatOutput_TableUsesNestedListPath(t *testing.T) {
	var buf bytes.Buffer
	data := []byte(`{"data":{"sessionList":{"nodes":[{"id":"s1","name":"alpha"},{"id":"s2","name":"beta"}]}}}`)
	err := FormatOutput(data, "table", &buf, OutputHints{
		ListPath:       "data.sessionList.nodes",
		DefaultColumns: []string{"id", "name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"ID", "NAME", "s1", "alpha", "s2", "beta"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table output missing %q:\n%s", want, got)
		}
	}
}

func TestRegisterFormatter(t *testing.T) {
	RegisterFormatter("custom", rawFormatter{})
	defer delete(formatters, "custom")

	var buf bytes.Buffer
	if err := FormatOutput([]byte("test"), "custom", &buf, OutputHints{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "test" {
		t.Errorf("got %q, want test", buf.String())
	}
}

func TestFormatterNames(t *testing.T) {
	RegisterFormatter("custom", rawFormatter{})
	defer delete(formatters, "custom")

	got := FormatterNames()
	want := []string{"table", "json", "yaml", "raw", "custom"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
