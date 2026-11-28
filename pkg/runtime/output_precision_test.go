package runtime

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFormatOutput_PreservesIntegerPrecision(t *testing.T) {
	for _, value := range []string{"9007199254740993", "9223372036854775807", "9223372036854775808", "18446744073709551615", "-9223372036854775808"} {
		for _, format := range []string{"json", "yaml", "table", ""} {
			t.Run(value+"/"+format, func(t *testing.T) {
				var out bytes.Buffer
				if err := FormatOutput([]byte(`{"id":`+value+`}`), format, &out, OutputHints{}); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), value) {
					t.Fatalf("integer changed: %s", out.String())
				}
				if format == "yaml" {
					var node yaml.Node
					if err := yaml.Unmarshal(out.Bytes(), &node); err != nil {
						t.Fatal(err)
					}
					scalar := node.Content[0].Content[1]
					if scalar.Tag != "!!int" || scalar.Value != value {
						t.Fatalf("YAML value = %#v", scalar)
					}
				}
			})
		}
	}
}
