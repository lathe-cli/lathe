package sourceconfig

import (
	"testing"

	"github.com/lathe-cli/lathe/internal/testutil"
)

func TestPublicRepoURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{raw: "https://user:secret@example.com/acme.git?token=x#frag", want: "https://example.com/acme.git"},
		{raw: "ssh://git@host/org/repo.git", want: "ssh://git@host/org/repo.git"},
		{raw: "ssh://git:secret@host/org/repo.git", want: "ssh://git@host/org/repo.git"},
		{raw: "github.com:org/repo.git", want: "github.com:org/repo.git"},
		{raw: "git@github.com:org/repo.git", want: "git@github.com:org/repo.git"},
		{raw: "/abs/upstream", want: ""},
		{raw: "file:///abs/upstream", want: ""},
		{raw: `C:\repo`, want: ""},
		{raw: "ftp://h/x", want: ""},
		{raw: "https://example.com/acme.git\n", want: ""},
		{raw: "https://example.com/acme`.git", want: ""},
		{raw: "user@host:path?token=x", want: ""},
		{raw: "/Users/me/repo@x:y", want: ""},
		{raw: "/tmp/a@host:path", want: ""},
		{raw: "https://example.com/acme.git?", want: "https://example.com/acme.git"},
	} {
		got := PublicRepoURL(tc.raw)
		testutil.Require(t, got == tc.want, "PublicRepoURL(%q) = %q, want %q", tc.raw, got, tc.want)
	}
}
