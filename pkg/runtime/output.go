package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func FormatOutput(data []byte, format string, w io.Writer, hints OutputHints) error {
	if len(data) == 0 {
		return nil
	}
	f, ok := lookupFormatter(format)
	if !ok {
		return fmt.Errorf("unknown output format: %s (supported: table|json|yaml|raw)", format)
	}
	return f.Format(w, data, hints)
}

func renderJSON(data []byte, w io.Writer) error {
	v, err := decodeNumberPreserving(data)
	if err != nil {
		_, werr := w.Write(data)
		return werr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderYAML(data []byte, w io.Writer) error {
	v, err := decodeNumberPreserving(data)
	if err != nil {
		_, werr := w.Write(data)
		return werr
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() { _ = enc.Close() }()
	return enc.Encode(yamlOutputValue(v))
}

func yamlOutputValue(v any) any {
	switch value := v.(type) {
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(value.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value.String()}
	case map[string]any:
		for k, child := range value {
			value[k] = yamlOutputValue(child)
		}
	case []any:
		for i, child := range value {
			value[i] = yamlOutputValue(child)
		}
	}
	return v
}
