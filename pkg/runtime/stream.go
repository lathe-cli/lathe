package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

var errStreamTerminal = errors.New("stream terminal event")

func collectStream(r io.Reader, hint *StreamingHint, live io.Writer, outcome *string) ([]byte, error) {
	policy := hint.Policy
	if policy.DataFormat != "json" {
		return nil, fmt.Errorf("unsupported stream data format %q", policy.DataFormat)
	}
	collected := map[string]any{}
	stopped := false
	handle := func(transportEvent string, data []byte) error {
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			return NewLatheError(CodeAPIError, ExitAPIError, fmt.Errorf("decode %s stream event: %w", hint.Strategy, err))
		}
		event := transportEvent
		if event == "" && policy.EventNamePath != "" {
			if value, ok := getNestedPath(payload, policy.EventNamePath); ok {
				event, _ = value.(string)
			}
		}
		for _, field := range policy.Collect.Fields {
			if !slices.Contains(field.Events, event) {
				continue
			}
			value, ok := streamFieldValue(payload, field)
			if ok {
				if err := reduceStreamField(collected, field, value); err != nil {
					return NewLatheError(CodeAPIError, ExitAPIError, err)
				}
			}
		}
		if live != nil && policy.Live != nil && slices.Contains(policy.Live.Events, event) {
			if value, ok := streamValue(payload, policy.Live.From); ok {
				if err := writeStreamValue(live, value); err != nil {
					return fmt.Errorf("write live stream: %w", err)
				}
			}
		}
		if slices.Contains(policy.Collect.ErrorEvents, event) {
			return NewLatheError(CodeAPIError, ExitAPIError, streamEventError(event, payload))
		}
		if slices.Contains(policy.Collect.PauseEvents, event) {
			*outcome = OperationOutcomePaused
			stopped = true
			return errStreamTerminal
		}
		if slices.Contains(policy.Collect.StopEvents, event) {
			stopped = true
			return errStreamTerminal
		}
		return nil
	}

	var err error
	switch hint.Strategy {
	case "sse":
		err = readSSE(r, handle)
	case "ndjson":
		err = readNDJSON(r, handle)
	default:
		err = fmt.Errorf("unsupported stream strategy %q", hint.Strategy)
	}
	if err != nil && !errors.Is(err, errStreamTerminal) {
		return nil, err
	}
	if policy.Collect.RequireStop && !stopped {
		return nil, NewLatheError(CodeAPIError, ExitAPIError, fmt.Errorf("%s stream ended without a terminal event", hint.Strategy))
	}
	return json.Marshal(collected)
}

func readSSE(r io.Reader, handle func(string, []byte) error) error {
	reader := bufio.NewReader(r)
	skipLF := false
	readLine := func() (string, error) {
		var line []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return string(line), err
			}
			if skipLF {
				skipLF = false
				if b == '\n' {
					continue
				}
			}
			switch b {
			case '\r':
				skipLF = true
				return string(line), nil
			case '\n':
				return string(line), nil
			default:
				line = append(line, b)
			}
		}
	}
	var event string
	var data [][]byte
	dispatch := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := handle(event, bytes.Join(data, []byte{'\n'}))
		event = ""
		data = data[:0]
		return err
	}
	for {
		line, err := readLine()
		if line == "" {
			if dispatchErr := dispatch(); dispatchErr != nil {
				return dispatchErr
			}
		} else if !strings.HasPrefix(line, ":") {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, []byte(value))
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			return fmt.Errorf("read sse stream: %w", err)
		}
	}
}

func readNDJSON(r io.Reader, handle func(string, []byte) error) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			if handleErr := handle("", line); handleErr != nil {
				return handleErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read ndjson stream: %w", err)
		}
	}
}

func streamFieldValue(payload any, field StreamFieldRule) (any, bool) {
	if field.From == "" {
		return field.Value, true
	}
	return streamValue(payload, field.From)
}

func streamValue(payload any, path string) (any, bool) {
	if path == "$" {
		return payload, true
	}
	return getNestedPath(payload, path)
}

func reduceStreamField(collected map[string]any, field StreamFieldRule, value any) error {
	existing, exists := getNestedPath(collected, field.To)
	switch field.Reduce {
	case "first":
		if exists {
			return nil
		}
	case "last":
	case "concat":
		part, ok := value.(string)
		if !ok {
			return fmt.Errorf("stream field %q concat value is %T, want string", field.To, value)
		}
		if exists {
			prefix, ok := existing.(string)
			if !ok {
				return fmt.Errorf("stream field %q concat target is %T, want string", field.To, existing)
			}
			value = prefix + part
		}
	case "append":
		items := []any{}
		if exists {
			var ok bool
			items, ok = existing.([]any)
			if !ok {
				return fmt.Errorf("stream field %q append target is %T, want array", field.To, existing)
			}
		}
		value = append(items, value)
	default:
		return fmt.Errorf("stream field %q has unsupported reducer %q", field.To, field.Reduce)
	}
	if err := setNestedPath(collected, field.To, value); err != nil {
		return fmt.Errorf("stream field %q: %w", field.To, err)
	}
	return nil
}

func writeStreamValue(w io.Writer, value any) error {
	if text, ok := value.(string); ok {
		_, err := io.WriteString(w, text)
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func streamEventError(event string, payload any) error {
	for _, path := range []string{"message", "error"} {
		if value, ok := getNestedPath(payload, path); ok {
			if text, ok := value.(string); ok && text != "" {
				return fmt.Errorf("stream event %q: %s", event, text)
			}
		}
	}
	return fmt.Errorf("stream event %q reported an error", event)
}
