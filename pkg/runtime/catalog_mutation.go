package runtime

import (
	"encoding/json"
	"strings"
)

func catalogMutation(spec CommandSpec) string {
	if kind := graphqlTemplateMutation(spec); kind != "" {
		return kind
	}
	switch strings.ToUpper(spec.Method) {
	case "GET", "HEAD":
		return MutationRead
	default:
		return MutationUnknown
	}
}

func catalogWorkflowMutation(spec WorkflowSpec) string {
	if len(spec.Steps) == 0 {
		return MutationUnknown
	}
	result := MutationRead
	for _, step := range spec.Steps {
		switch catalogMutation(step.Operation) {
		case MutationWrite:
			return MutationWrite
		case MutationUnknown:
			result = MutationUnknown
		}
	}
	return result
}

func graphqlTemplateMutation(spec CommandSpec) string {
	if spec.RequestBody == nil || spec.RequestBody.Template == "" {
		return ""
	}
	var envelope struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(spec.RequestBody.Template), &envelope); err != nil || strings.TrimSpace(envelope.Query) == "" {
		return ""
	}
	return graphqlDocMutation(envelope.Query)
}

func graphqlDocMutation(doc string) string {
	s := strings.TrimSpace(doc)
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "#") {
			break
		}
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
			continue
		}
		return ""
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "mutation") && graphqlKeywordBoundary(s, len("mutation")):
		return MutationWrite
	case strings.HasPrefix(lower, "query") && graphqlKeywordBoundary(s, len("query")):
		return MutationRead
	case strings.HasPrefix(lower, "subscription") && graphqlKeywordBoundary(s, len("subscription")):
		return MutationRead
	case strings.HasPrefix(s, "{"):
		return MutationRead
	default:
		return ""
	}
}

func graphqlKeywordBoundary(s string, n int) bool {
	if len(s) < n {
		return false
	}
	if len(s) == n {
		return true
	}
	switch s[n] {
	case ' ', '\t', '\n', '\r', '(', '{', '#':
		return true
	default:
		return false
	}
}
