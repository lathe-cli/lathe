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
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/lathe-cli/lathe/pkg/config"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

var (
	updateGitHubAPIBaseURL = "https://api.github.com"
	updateHTTPClient       = &http.Client{Timeout: 30 * time.Second}
	installUpdate          = replaceExecutable
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
	release, err := fetchLatestGitHubRelease(cmd.Context(), *m.Update.GitHub)
	if err != nil {
		return err
	}
	if release.TagName == "" {
		return fmt.Errorf("latest release has no tag_name")
	}
	cmp, err := compareVersions(Version, release.TagName)
	if err != nil {
		return err
	}
	if cmp == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date (%s)\n", m.CLI.Name, release.TagName)
		return nil
	}
	if cmp > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is newer than latest release (%s > %s)\n", m.CLI.Name, Version, release.TagName)
		return nil
	}
	if !yes && !confirmUpdate(cmd, m.CLI.Name, Version, release.TagName) {
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
	return nil
}

func fetchLatestGitHubRelease(ctx context.Context, gh config.GitHubUpdate) (githubRelease, error) {
	u := strings.TrimRight(updateGitHubAPIBaseURL, "/") + "/repos/" + neturl.PathEscape(gh.Owner) + "/" + neturl.PathEscape(gh.Repo) + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("fetch latest release: GitHub returned %s", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
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
	resp, err := updateHTTPClient.Do(req)
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
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
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
	tr := tar.NewReader(gz)
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
		if f.FileInfo().IsDir() || !binaryNameMatches(f.Name, cliName) {
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
	_, copyErr := io.Copy(f, r)
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
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace %s: %w", target, err)
	}
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
		if a.parts[i] < b.parts[i] {
			return -1, nil
		}
		if a.parts[i] > b.parts[i] {
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
	return strings.Compare(a.suffix, b.suffix), nil
}

type parsedVersion struct {
	parts  [3]int
	suffix string
}

func parseVersion(v string) (parsedVersion, error) {
	v = cleanVersion(v)
	core, suffix, _ := strings.Cut(v, "-")
	fields := strings.Split(core, ".")
	if len(fields) == 0 || len(fields) > 3 {
		return parsedVersion{}, fmt.Errorf("invalid version")
	}
	var out parsedVersion
	out.suffix = suffix
	for i, field := range fields {
		if field == "" {
			return parsedVersion{}, fmt.Errorf("invalid version")
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return parsedVersion{}, err
		}
		out.parts[i] = n
	}
	return out, nil
}

func cleanVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
