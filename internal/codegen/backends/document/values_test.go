package document

import "testing"

func TestMergeTags_DescriptionPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, first, next, want string }{
		{"later description", "", "Manage users", "Manage users"},
		{"later empty", "Manage users", "", "Manage users"},
		{"conflicting description", "Manage users", "Other users", "Manage users"},
		{"matching description", "Manage users", "Manage users", "Manage users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeTags([]Tag{{Name: "Users", Description: tc.first}}, []Tag{{Name: "Users", Description: tc.next}}, "users", "b.yaml")
			if len(got) != 1 || got[0].Description != tc.want {
				t.Fatalf("tags = %v, want description %q", got, tc.want)
			}
		})
	}
}
