package runtime

import (
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func FormatOutput(data []byte, format string, w io.Writer, hints OutputHints) error {
	if len(data) == 0 {
		return nil
	}
	if err := validateOutputFormat(format); err != nil {
		return err
	}
	f := formatters[format]
	return f.Format(w, data, hints)
}

func validateOutputFormat(format string) error {
	if _, ok := formatters[format]; !ok {
		return fmt.Errorf("unknown output format: %s (supported: table|json|yaml|raw)", format)
	}
	return nil
}

func renderJSON(data []byte, w io.Writer) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		_, werr := w.Write(data)
		return werr
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderYAML(data []byte, w io.Writer) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		_, werr := w.Write(data)
		return werr
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() { _ = enc.Close() }()
	return enc.Encode(v)
}
