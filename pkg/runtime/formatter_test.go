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

func TestFormatOutput_TableUsesConfiguredColumnLabels(t *testing.T) {
	var buf bytes.Buffer
	data := []byte(`{"items":[{"resourceId":"r1","createdAt":"2026-08-28","status":"active","details":{"owner":{"profile":{"contact":{"display name":"Alice Smith","user-name":"alice"}}}}}]}`)
	err := FormatOutput(data, "table", &buf, OutputHints{
		ListPath:       "items",
		DefaultColumns: []string{"resourceId", "createdAt", "status", "details.owner.profile.contact.display name", "details.owner.profile.contact.user-name"},
		ColumnLabels: map[string]string{
			"resourceId": "Resource ID",
			"createdAt":  "Created at",
			"status":     "Status",
			"details.owner.profile.contact.display name": "Display name",
			"details.owner.profile.contact.user-name":    "User",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"Resource ID", "Created at", "Status", "Display name", "User", "r1", "2026-08-28", "active", "Alice Smith", "alice"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatOutput_TableUsesExactCurrencyFormats(t *testing.T) {
	var buf bytes.Buffer
	data := []byte(`{"items":[{"amount":1000000},{"amount":1234567},{"amount":1},{"amount":-5000000},{"amount":9007199254740993},{"amount":"2500000"},{"amount":1.25}]}`)
	err := FormatOutput(data, "table", &buf, OutputHints{
		ListPath:       "items",
		DefaultColumns: []string{"amount"},
		ColumnFormats: map[string]ColumnFormat{
			"amount": {
				Kind:              "currency",
				Currency:          "USD",
				SourceScale:       6,
				Grouping:          true,
				MinFractionDigits: 2,
				MaxFractionDigits: 6,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "AMOUNT\n$1.00\n$1.234567\n$0.000001\n-$5.00\n$9,007,199,254.740993\n$2.50\n1.25\n"
	if buf.String() != want {
		t.Fatalf("table output = %q, want %q", buf.String(), want)
	}
}

func TestFormatOutput_CurrencyFormatIsTableOnly(t *testing.T) {
	data := []byte(`{"amount":1000000}`)
	hints := OutputHints{
		DefaultColumns: []string{"amount"},
		ColumnFormats: map[string]ColumnFormat{
			"amount": {Kind: "currency", Currency: "USD", SourceScale: 6, MinFractionDigits: 2, MaxFractionDigits: 6},
		},
	}
	for _, format := range []string{"json", "raw"} {
		var buf bytes.Buffer
		if err := FormatOutput(data, format, &buf, hints); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if !strings.Contains(buf.String(), "1000000") || strings.Contains(buf.String(), "$1.00") {
			t.Fatalf("%s output changed: %q", format, buf.String())
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
