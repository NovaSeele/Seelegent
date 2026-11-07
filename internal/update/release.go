package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Release sources.
const (
	// Repo is the GitHub repository the releases come from.
	Repo = "NovaSeele/Seelegent"
	// LatestURL is the GitHub API address of the latest release.
	LatestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"
	// EnvUpdateURL overrides the release metadata address (as in the old version), for example a local server in
	// tests: http is accepted only for loopback hosts.
	EnvUpdateURL = "SEELEGENT_UPDATE_URL"
	// ChecksumsName is the checksum file every release carries (goreleaser, SHA-256).
	ChecksumsName = "checksums.txt"
	// ProjectName is the archive and binary name.
	ProjectName = "seelegent"
)

// Size limits of what is downloaded.
const (
	maxMetadata  = 4 << 20
	maxChecksums = 1 << 20
	maxArchive   = 512 << 20
	maxRedirects = 5
)

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release is the part of a GitHub release the updater reads.
type Release struct {
	Tag     string  `json:"tag_name"`
	Name    string  `json:"name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
	// Version is Tag (else Name) parsed.
	Version Version `json:"-"`
}

// MetadataURL returns the address of the release metadata: override (SEELEGENT_UPDATE_URL) or the GitHub API, for
// the latest release or, when tag is set, that release. An address ending in /releases/latest asks for the tag at
// /releases/tags/<tag> instead; any other override is used as it is, and the release it names must be tag.
func MetadataURL(override, tag string) string {
	base := strings.TrimSpace(override)
	if base == "" {
		base = LatestURL
	}
	if tag == "" {
		return base
	}
	if trimmed, ok := strings.CutSuffix(strings.TrimRight(base, "/"), "/releases/latest"); ok {
		return trimmed + "/releases/tags/" + url.PathEscape(tag)
	}
	return base
}

// NormalizeTag turns "1.2.3" or "v1.2.3" into "v1.2.3"; it fails for anything that is not a version.
func NormalizeTag(s string) (string, error) {
	v, err := ParseVersion(s)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// ErrInsecureURL is returned for an address that is neither https nor http on a loopback host (old C-B15: the old
// updater followed redirects to plain http).
var ErrInsecureURL = errors.New("only https addresses (or http on this machine) are allowed")

// checkURL accepts https, and http only for a loopback host (a local test server).
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("address %q: %w", raw, err)
	}
	if u.User != nil { // user@host: never needed for a release, and easy to misread as host
		return fmt.Errorf("%w: %s", ErrInsecureURL, u.Redacted())
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrInsecureURL, u.Redacted())
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// NewHTTPClient returns a client that refuses redirects to addresses checkURL does not accept and stops after a few
// redirects. It has no overall timeout: every request carries a context.
func NewHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		err := checkURL(req.URL.String())
		return err
	}}
}

// get fetches raw (checked by checkURL) and returns the body, which the caller closes.
func (u *Updater) get(ctx context.Context, raw, accept string) (io.ReadCloser, error) {
	if err := checkURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "seelegent/"+u.o.Current)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", raw, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("download %s: %w (HTTP 404)", raw, ErrNotFound)
		}
		return nil, fmt.Errorf("download %s: HTTP %d", raw, resp.StatusCode)
	}
	return resp.Body, nil
}

// ErrNotFound is returned when the release (or one of its files) does not exist.
var ErrNotFound = errors.New("not found")

// readAll reads at most limit bytes of the body of raw.
func (u *Updater) readAll(ctx context.Context, raw, accept string, limit int64) ([]byte, error) {
	body, err := u.get(ctx, raw, accept)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", raw, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", raw, limit)
	}
	return data, nil
}

// download writes the body of raw into the new file path (at most limit bytes).
func (u *Updater) download(ctx context.Context, raw, path string, limit int64) error {
	body, err := u.get(ctx, raw, "application/octet-stream")
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(body, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", raw, err)
	}
	if n > limit {
		return fmt.Errorf("download %s: larger than %d bytes", raw, limit)
	}
	return nil
}

// fetchRelease reads the release metadata (the latest release, or tag when set).
func (u *Updater) fetchRelease(ctx context.Context, tag string) (Release, error) {
	src := MetadataURL(u.o.MetadataURL, tag)
	data, err := u.readAll(ctx, src, "application/vnd.github+json", maxMetadata)
	if err != nil {
		if tag != "" && errors.Is(err, ErrNotFound) {
			return Release{}, fmt.Errorf("release %s: %w", tag, err)
		}
		return Release{}, fmt.Errorf("read the release information: %w", err)
	}
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return Release{}, fmt.Errorf("read the release information from %s: %w", src, err)
	}
	name := r.Tag
	if name == "" {
		name = r.Name
	}
	if r.Version, err = ParseVersion(name); err != nil {
		return Release{}, fmt.Errorf("the release at %s has no valid version: %w", src, err)
	}
	if tag != "" && r.Version.String() != tag {
		return Release{}, fmt.Errorf("the release at %s is %s, not %s", src, r.Version, tag)
	}
	return r, nil
}

// archiveName is the archive of goos/goarch in a release of version v (goreleaser's name template).
func archiveName(v Version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", ProjectName, strings.TrimPrefix(v.String(), "v"), goos, goarch, ext)
}

// assets returns the archive for this platform and the checksum file of r.
func (r Release) assets(goos, goarch string) (archive, sums Asset, err error) {
	want := archiveName(r.Version, goos, goarch)
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			archive = a
		case ChecksumsName:
			sums = a
		}
	}
	if archive.URL == "" {
		return Asset{}, Asset{}, fmt.Errorf("release %s has no %s (no build for %s/%s?)", r.Version, want, goos, goarch)
	}
	if sums.URL == "" {
		return Asset{}, Asset{}, fmt.Errorf("release %s has no %s; refusing to install a file that cannot be "+
			"verified", r.Version, ChecksumsName)
	}
	return archive, sums, nil
}
