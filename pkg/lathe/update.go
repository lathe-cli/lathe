package lathe

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

var (
	updateGitHubAPIBaseURL  = "https://api.github.com"
	defaultUpdateHTTPClient = &http.Client{Timeout: 30 * time.Second}
	updateHTTPClient        = defaultUpdateHTTPClient
	installUpdate           = replaceExecutable
	updateVersionInfo       = VersionInfo
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func updateCmd(m *config.Manifest) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this CLI from GitHub Releases",
		Args:  runtime.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGitHubUpdate(cmd, m, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Update without prompting")
	return cmd
}

func runGitHubUpdate(cmd *cobra.Command, m *config.Manifest, yes bool) error {
	currentVersion, _, _ := updateVersionInfo()
	release, err := fetchLatestGitHubRelease(cmd.Context(), *m.Update.GitHub)
	if err != nil {
		return err
	}
	if release.TagName == "" {
		return fmt.Errorf("latest release has no tag_name")
	}
	cmp, err := compareVersions(currentVersion, release.TagName)
	if err != nil {
		return err
	}
	if cmp == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date (%s)\n", m.CLI.Name, release.TagName)
		printSkillStatusHint(cmd, m.CLI.Name)
		return nil
	}
	if cmp > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is newer than latest release (%s > %s)\n", m.CLI.Name, currentVersion, release.TagName)
		return nil
	}
	if !yes && !confirmUpdate(cmd, m.CLI.Name, currentVersion, release.TagName) {
		fmt.Fprintln(cmd.OutOrStdout(), "Update cancelled.")
		return nil
	}

	assetName, err := renderUpdateAssetName(m.CLI.Name, m.Update.GitHub.Asset, release.TagName)
	if err != nil {
		return err
	}
	asset, ok := findReleaseAsset(release, assetName)
	if !ok {
		return fmt.Errorf("release %s has no asset %q", release.TagName, assetName)
	}
	digest, ok := sha256Digest(asset.Digest)
	if !ok {
		return fmt.Errorf("release asset %q is missing a sha256 digest", asset.Name)
	}
	archivePath, err := downloadReleaseAsset(cmd.Context(), asset, digest)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archivePath) }()

	binaryPath, err := updatePayloadPath(archivePath, asset.Name, m.CLI.Name)
	if err != nil {
		return err
	}
	if binaryPath != archivePath {
		defer func() { _ = os.Remove(binaryPath) }()
	}
	if err := installUpdate(binaryPath); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated %s to %s\n", m.CLI.Name, release.TagName)
	printSkillStatusHint(cmd, m.CLI.Name)
	return nil
}

func printSkillStatusHint(cmd *cobra.Command, cli string) {
	if runtime.HasCapability(cmd.Root(), runtime.CapabilitySkillBundle) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Run `%s skill status` to compare installed Skills with this binary's bundle.\n", cli)
	}
}

const (
	maxUpdateReleaseBytes  = 4 << 20
	maxUpdateArchiveBytes  = 256 << 20
	maxUpdateBinaryBytes   = 128 << 20
	maxUpdateExpandedBytes = 512 << 20
)

func validateUpdateURL(u *neturl.URL) error {
	if u.User != nil || u.Hostname() == "" {
		return fmt.Errorf("update URL must have a host and no credentials")
	}
	if u.Scheme == "https" {
		return nil
	}
	if updateHTTPClient != defaultUpdateHTTPClient && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
		return nil
	}
	return fmt.Errorf("update URL must use HTTPS")
}

func updateRequest(req *http.Request) (*http.Response, error) {
	if err := validateUpdateURL(req.URL); err != nil {
		return nil, err
	}
	client := *updateHTTPClient
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many update redirects")
		}
		if err := validateUpdateURL(next.URL); err != nil {
			return err
		}
		from := via[len(via)-1].URL
		sameOrigin := strings.EqualFold(from.Scheme, next.URL.Scheme) && strings.EqualFold(from.Host, next.URL.Host)
		githubAsset := from.Scheme == "https" && next.URL.Scheme == "https" && from.Host == "github.com" && (next.URL.Host == "release-assets.githubusercontent.com" || next.URL.Host == "objects.githubusercontent.com")
		if !sameOrigin && !githubAsset {
			return fmt.Errorf("unsafe update redirect")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	return client.Do(req)
}

func fetchLatestGitHubRelease(ctx context.Context, gh config.GitHubUpdate) (githubRelease, error) {
	for _, part := range []string{gh.Owner, gh.Repo} {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." || strings.ContainsAny(part, `/\`) {
			return githubRelease{}, fmt.Errorf("invalid GitHub update owner or repository")
		}
	}
	u := strings.TrimRight(updateGitHubAPIBaseURL, "/") + "/repos/" + neturl.PathEscape(gh.Owner) + "/" + neturl.PathEscape(gh.Repo) + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := updateRequest(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("fetch latest release: GitHub returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdateReleaseBytes+1))
	if err != nil {
		return githubRelease{}, fmt.Errorf("read latest release: %w", err)
	}
	if len(body) > maxUpdateReleaseBytes {
		return githubRelease{}, fmt.Errorf("latest release exceeds size limit")
	}
	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return githubRelease{}, fmt.Errorf("parse latest release: %w", err)
	}
	return release, nil
}

func confirmUpdate(cmd *cobra.Command, name, current, latest string) bool {
	fmt.Fprintf(cmd.OutOrStdout(), "Update %s from %s to %s? [y/N] ", name, current, latest)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	answer := strings.TrimSpace(line)
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

func renderUpdateAssetName(name, pattern, tag string) (string, error) {
	t, err := template.New("asset").Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("parse update asset template: %w", err)
	}
	var b strings.Builder
	data := struct {
		Name    string
		Version string
		Tag     string
		OS      string
		Arch    string
	}{
		Name:    name,
		Version: cleanVersion(tag),
		Tag:     tag,
		OS:      stdruntime.GOOS,
		Arch:    stdruntime.GOARCH,
	}
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render update asset template: %w", err)
	}
	return b.String(), nil
}

func findReleaseAsset(release githubRelease, name string) (githubAsset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubAsset{}, false
}

func sha256Digest(digest string) (string, bool) {
	alg, value, ok := strings.Cut(digest, ":")
	if !ok || !strings.EqualFold(alg, "sha256") {
		return "", false
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != sha256.Size {
		return "", false
	}
	return strings.ToLower(value), true
}

func downloadReleaseAsset(ctx context.Context, asset githubAsset, expectedDigest string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := updateRequest(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: server returned %s", asset.Name, resp.Status)
	}
	f, err := os.CreateTemp("", "lathe-update-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxUpdateArchiveBytes+1))
	if copyErr == nil && n > maxUpdateArchiveBytes {
		copyErr = fmt.Errorf("update archive exceeds size limit")
	}
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("download %s: %w", asset.Name, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != expectedDigest {
		_ = os.Remove(path)
		return "", fmt.Errorf("download %s: sha256 mismatch", asset.Name)
	}
	return path, nil
}

func updatePayloadPath(path, assetName, cliName string) (string, error) {
	lower := strings.ToLower(assetName)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGzBinary(path, cliName)
	case strings.HasSuffix(lower, ".zip"):
		return extractZipBinary(path, cliName)
	default:
		return path, nil
	}
}

func extractTarGzBinary(path, cliName string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(io.LimitReader(gz, maxUpdateExpandedBytes+1))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if h.Typeflag == tar.TypeReg && binaryNameMatches(h.Name, cliName) {
			return writeUpdatePayload(tr, h.FileInfo().Mode())
		}
	}
	return "", fmt.Errorf("archive does not contain %q", cliName)
}

func extractZipBinary(path, cliName string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if !f.Mode().IsRegular() || !binaryNameMatches(f.Name, cliName) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out, werr := writeUpdatePayload(rc, f.FileInfo().Mode())
		cerr := rc.Close()
		if werr != nil {
			return "", werr
		}
		if cerr != nil {
			_ = os.Remove(out)
			return "", cerr
		}
		return out, nil
	}
	return "", fmt.Errorf("archive does not contain %q", cliName)
}

func binaryNameMatches(name, cliName string) bool {
	base := filepath.Base(name)
	return base == cliName || base == cliName+".exe"
}

func writeUpdatePayload(r io.Reader, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp("", "lathe-update-bin-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	n, copyErr := io.Copy(f, io.LimitReader(r, maxUpdateBinaryBytes+1))
	if copyErr == nil && n > maxUpdateBinaryBytes {
		copyErr = fmt.Errorf("update binary exceeds size limit")
	}
	if mode == 0 {
		mode = 0o755
	}
	chmodErr := f.Chmod(mode.Perm())
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return "", copyErr
	}
	if chmodErr != nil {
		_ = os.Remove(path)
		return "", chmodErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", closeErr
	}
	return path, nil
}

func replaceExecutable(src string) error {
	target, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, in)
	chmodErr := tmp.Chmod(info.Mode().Perm())
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return copyErr
	}
	if chmodErr != nil {
		_ = os.Remove(tmpPath)
		return chmodErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := replaceUpdateFile(tmpPath, target, stdruntime.GOOS == "windows", os.Rename); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}

func replaceUpdateFile(src, target string, windows bool, rename func(string, string) error) error {
	err := rename(src, target)
	if err == nil || !windows {
		return err
	}
	backup, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.old")
	if err != nil {
		return err
	}
	old := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(old)
		return err
	}
	if err := os.Remove(old); err != nil {
		return err
	}
	if err := rename(target, old); err != nil {
		return err
	}
	if err := rename(src, target); err != nil {
		if restoreErr := rename(old, target); restoreErr != nil {
			return fmt.Errorf("install update: %v; restore executable from %s: %w", err, old, restoreErr)
		}
		return err
	}
	_ = os.Remove(old)
	return nil
}

func compareVersions(current, latest string) (int, error) {
	a, err := parseVersion(current)
	if err != nil {
		return 0, fmt.Errorf("current version %q cannot be compared; build with RunOptions.Version or ldflags to enable update", current)
	}
	b, err := parseVersion(latest)
	if err != nil {
		return 0, fmt.Errorf("latest version %q cannot be compared", latest)
	}
	for i := range a.parts {
		if compareNumericVersion(a.parts[i], b.parts[i]) < 0 {
			return -1, nil
		}
		if compareNumericVersion(a.parts[i], b.parts[i]) > 0 {
			return 1, nil
		}
	}
	if a.suffix == b.suffix {
		return 0, nil
	}
	if a.suffix != "" && b.suffix == "" {
		return -1, nil
	}
	if a.suffix == "" && b.suffix != "" {
		return 1, nil
	}
	left, right := strings.Split(a.suffix, "."), strings.Split(b.suffix, ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] == right[i] {
			continue
		}
		ln, rn := numericVersion(left[i]), numericVersion(right[i])
		if ln && rn {
			return compareNumericVersion(left[i], right[i]), nil
		}
		if ln {
			return -1, nil
		}
		if rn {
			return 1, nil
		}
		return strings.Compare(left[i], right[i]), nil
	}
	if len(left) < len(right) {
		return -1, nil
	}
	return 1, nil
}

type parsedVersion struct {
	parts  [3]string
	suffix string
}

func numericVersion(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareNumericVersion(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func validVersionIdentifiers(s string, prerelease bool) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" || (prerelease && numericVersion(part) && len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, r := range part {
			valid := r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-'
			if !valid {
				return false
			}
		}
	}
	return true
}

func parseVersion(v string) (parsedVersion, error) {
	v = cleanVersion(v)
	v, build, hasBuild := strings.Cut(v, "+")
	if hasBuild && !validVersionIdentifiers(build, false) {
		return parsedVersion{}, fmt.Errorf("invalid version")
	}
	core, suffix, hasSuffix := strings.Cut(v, "-")
	if hasSuffix && !validVersionIdentifiers(suffix, true) {
		return parsedVersion{}, fmt.Errorf("invalid version")
	}
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return parsedVersion{}, fmt.Errorf("invalid version")
	}
	out := parsedVersion{suffix: suffix}
	for i, field := range fields {
		if !numericVersion(field) || (len(field) > 1 && field[0] == '0') {
			return parsedVersion{}, fmt.Errorf("invalid version")
		}
		out.parts[i] = field
	}
	return out, nil
}

func cleanVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
