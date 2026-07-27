package specsync

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ensureRepo publishes a complete checkout atomically so concurrent syncs
// never mutate the same worktree.
func ensureRepo(workDir, repoURL, tag string) (string, error) {
	gitDir := filepath.Join(workDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(workDir), 0o755); err != nil {
			return "", err
		}
		cloneDir, err := os.MkdirTemp(filepath.Dir(workDir), ".checkout-*")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(cloneDir) }()
		fmt.Fprintf(os.Stderr, "=> clone %s\n", repoURL)
		cmd := exec.Command("git", "clone", "--filter=blob:none", "--quiet", repoURL, cloneDir)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git clone: %w", err)
		}
		checkout := exec.Command("git", "-C", cloneDir, "-c", "advice.detachedHead=false", "checkout", "--quiet", tag)
		checkout.Stderr = os.Stderr
		if err := checkout.Run(); err != nil {
			return "", fmt.Errorf("git checkout %s: %w", tag, err)
		}
		sha, err := repoSHA(cloneDir)
		if err != nil {
			return "", err
		}
		if err := os.Rename(cloneDir, workDir); err != nil {
			if _, statErr := os.Stat(gitDir); statErr != nil {
				return "", fmt.Errorf("publish git clone: %w", err)
			}
			return verifiedRepoSHA(workDir, repoURL, tag)
		}
		return sha, nil
	} else if err != nil {
		return "", err
	}
	return verifiedRepoSHA(workDir, repoURL, tag)
}

func verifiedRepoSHA(workDir, repoURL, ref string) (string, error) {
	sha, err := repoSHA(workDir)
	if err != nil {
		return "", err
	}
	want, err := remoteRefSHA(repoURL, ref)
	if err != nil {
		return "", err
	}
	if sha != want {
		return "", fmt.Errorf("configured ref %q changed upstream: cached checkout is %s, remote resolves to %s", ref, sha, want)
	}
	return sha, nil
}

func remoteRefSHA(repoURL, ref string) (string, error) {
	if len(ref) == 40 && isHex(ref) {
		return ref, nil
	}
	tagRef := ref
	if !strings.HasPrefix(tagRef, "refs/tags/") {
		tagRef = "refs/tags/" + tagRef
	}
	peeledRef := tagRef + "^{}"
	var out bytes.Buffer
	cmd := exec.Command("git", "ls-remote", repoURL, tagRef, peeledRef)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote %s: %w", ref, err)
	}
	var direct, peeled string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[1] {
		case tagRef:
			direct = fields[0]
		case peeledRef:
			peeled = fields[0]
		}
	}
	sha := peeled
	if sha == "" {
		sha = direct
	}
	if len(sha) != 40 || !isHex(sha) {
		return "", fmt.Errorf("git ref %q not found in remote", ref)
	}
	return sha, nil
}

func repoSHA(workDir string) (string, error) {
	var out bytes.Buffer
	rev := exec.Command("git", "-C", workDir, "rev-parse", "HEAD")
	rev.Stdout = &out
	rev.Stderr = os.Stderr
	if err := rev.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	sha := string(bytes.TrimSpace(out.Bytes()))
	if len(sha) != 40 || !isHex(sha) {
		return "", fmt.Errorf("unexpected rev-parse output %q", sha)
	}
	return sha, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
