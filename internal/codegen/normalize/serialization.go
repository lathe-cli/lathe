package normalize

import (
	"fmt"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

type ParameterError struct {
	OperationID string
	Method      string
	Path        string
	Name        string
	In          string
	Style       string
	reason      string
}

func (e ParameterError) Error() string {
	return fmt.Sprintf("%s %s (%s): parameter %q in %s style %q: %s; fix the specification or exclude the operation", e.Method, e.Path, e.OperationID, e.Name, e.In, e.Style, e.reason)
}

func ValidateParameters(mod *rawir.RawModule) []ParameterError {
	if mod == nil {
		return nil
	}
	var out []ParameterError
	for _, op := range mod.Operations {
		id := op.OperationID
		if id == "" {
			id = synthOperationID(op.Method, op.Path)
		}
		for _, p := range op.Parameters {
			style, _, err := serialization(p)
			if err == nil {
				continue
			}
			out = append(out, ParameterError{
				OperationID: id,
				Method:      op.Method,
				Path:        joinBasePath(op.ServerBasePath, op.Path),
				Name:        p.Name,
				In:          p.In,
				Style:       style,
				reason:      err.Error(),
			})
		}
	}
	return out
}

func serialization(p rawir.RawParameter) (string, bool, error) {
	if p.In != runtime.InPath && p.In != runtime.InQuery && p.In != runtime.InHeader && p.In != runtime.InCookie {
		return "", false, nil
	}
	style := p.Style
	if style == "" {
		switch p.In {
		case runtime.InPath, runtime.InHeader:
			style = "simple"
		default:
			style = "form"
		}
	}
	explode := style == "form"
	if p.Explode != nil {
		explode = *p.Explode
	}
	if p.Type == "object" {
		return style, explode, fmt.Errorf("object parameters are unsupported")
	}
	if !styleAllowed(p.In, style) {
		return style, explode, fmt.Errorf("style is unsupported for this location")
	}
	if (style == "spaceDelimited" || style == "pipeDelimited") && p.Type != "array" {
		return style, explode, fmt.Errorf("style requires an array")
	}
	if p.In == runtime.InCookie && p.Type == "array" {
		return style, explode, fmt.Errorf("array cookies are unsupported")
	}
	return style, explode, nil
}

func styleAllowed(in, style string) bool {
	switch in {
	case runtime.InPath:
		return style == "simple" || style == "label" || style == "matrix"
	case runtime.InQuery:
		return style == "form" || style == "spaceDelimited" || style == "pipeDelimited"
	case runtime.InHeader:
		return style == "simple"
	case runtime.InCookie:
		return style == "form"
	default:
		return true
	}
}

func defaultSerialization(in, style string, explode bool) bool {
	switch in {
	case runtime.InPath, runtime.InHeader:
		return style == "simple" && !explode
	case runtime.InQuery, runtime.InCookie:
		return style == "form" && explode
	default:
		return true
	}
}
