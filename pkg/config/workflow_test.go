package config

import (
	"strings"
	"testing"
)

func TestNormalizeWorkflowRejectsReservedInputFlags(t *testing.T) {
	for _, flag := range []string{"output", "hostname", "insecure", "debug", "help"} {
		for _, explicit := range []bool{false, true} {
			input := WorkflowInput{Name: flag}
			if explicit {
				input = WorkflowInput{Name: "value", Flag: flag}
			}
			workflow := WorkflowInfo{Commands: []WorkflowCommand{{Use: "doctor", Inputs: []WorkflowInput{input}, Steps: []WorkflowStep{{ID: "get", Uses: "get"}}}}}
			if err := normalizeWorkflow(&workflow); err == nil || !strings.Contains(err.Error(), "flag") {
				t.Errorf("flag %q explicit %t: expected reserved flag error, got %v", flag, explicit, err)
			}
		}
	}
}
