// Package update is Toru's in-app updater: Check for Updates + one-click
// auto-update, modeled on the wc3-forge pattern.
//
// It queries the GitHub Releases API for the latest release, compares the tag
// against the running version (injected via -ldflags -X main.version), finds the
// Windows NSIS installer asset, verifies it against the release's SHA256SUMS,
// then runs the (per-user, silent) installer and quits so the running toru.exe
// is unlocked for NSIS to overwrite.
//
// We deliberately do NOT use Wails v3's built-in pkg/updater: it does a bare
// in-place exe swap, which would leave an NSIS-installed app's registry /
// uninstall entries / shortcuts stale. Running the real installer keeps the
// install coherent.
//
// JS binding name: UpdateService.*
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventUpdateInstalling is emitted (with the target version string) right before
// the silent installer launches and the app quits to update, so any open window
// can show an "Updating…" state instead of just vanishing.
const EventUpdateInstalling = "update:installing"

// UpdateInfo describes an available update (returned to the frontend).
type UpdateInfo struct {
	Version     string `json:"version"`     // e.g. "1.3.0" (tag without leading v)
	Notes       string `json:"notes"`       // release body (markdown)
	AssetURL    string `json:"assetUrl"`    // browser_download_url of the installer
	AssetName   string `json:"assetName"`   // installer filename
	SHA256      string `json:"sha256"`      // expected hash from SHA256SUMS ("" if absent)
	PublishedAt string `json:"publishedAt"` // ISO-8601
}

// UpdateService is the Wails-bound updater API.
type UpdateService struct {
	repo       string // "owner/repo", e.g. "StephenSHorton/toru"
	current    string // running version (ldflags-injected; "dev" in non-release builds)
	client     *http.Client
	app        *application.App
	installing atomic.Bool // set once an install is in flight; blocks a second install
}

// New returns an UpdateService for the given GitHub repo and running version.
func New(repo, currentVersion string) *UpdateService {
	return &UpdateService{
		repo:    repo,
		current: currentVersion,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// SetApp injects the running app (used to quit before the installer runs).
func (s *UpdateService) SetApp(app *application.App) { s.app = app }

// GetVersion returns the running version string.
func (s *UpdateService) GetVersion() string { return s.current }

// AutoUpdate is Toru's "always up to date" policy: it checks for a newer release
// and, if one exists, downloads, verifies, and silently installs it — no prompt,
// no opt-out. Keeping Toru current is part of using it. The silent installer
// overwrites toru.exe and relaunches it (see build/windows/nsis/project.nsi), so
// from the user's view the app briefly closes and reopens on the new version.
//
// It runs as a fire-and-forget goroutine on startup (the app is idle then, so no
// capture/recording is interrupted). All errors are logged and swallowed — a
// failed or offline update check must never block startup. Concurrent callers
// (e.g. a manual "Check for Updates" click racing this) are deduped by the
// install guard in DownloadAndInstall.
func (s *UpdateService) AutoUpdate() {
	info, err := s.CheckForUpdate()
	if err != nil {
		log.Printf("toru: auto-update check failed: %v", err)
		return
	}
	if info == nil {
		return // already up to date (or a dev build)
	}
	log.Printf("toru: auto-updating to v%s", info.Version)
	if err := s.DownloadAndInstall(*info); err != nil {
		log.Printf("toru: auto-update install failed: %v", err)
	}
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

func (s *UpdateService) latestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", s.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no releases published yet
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github releases: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// CheckForUpdate returns the available update, or nil if the app is up to date
// (or running a "dev" build, which never reports updates).
func (s *UpdateService) CheckForUpdate() (*UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := s.latestRelease(ctx)
	if err != nil || rel == nil {
		return nil, err
	}
	if !isNewer(rel.TagName, s.current) {
		return nil, nil
	}

	var installer ghAsset
	var sumsURL string
	for _, a := range rel.Assets {
		n := strings.ToLower(a.Name)
		if strings.HasSuffix(n, "-installer.exe") && strings.Contains(n, "windows") {
			installer = a
		}
		if n == "sha256sums" {
			sumsURL = a.URL
		}
	}
	if installer.URL == "" {
		return nil, fmt.Errorf("release %s has no windows installer asset", rel.TagName)
	}

	var sum string
	if sumsURL != "" {
		sum, _ = s.fetchChecksum(ctx, sumsURL, installer.Name) // best-effort
	}

	return &UpdateInfo{
		Version:     strings.TrimPrefix(rel.TagName, "v"),
		Notes:       rel.Body,
		AssetURL:    installer.URL,
		AssetName:   installer.Name,
		SHA256:      sum,
		PublishedAt: rel.PublishedAt,
	}, nil
}

func (s *UpdateService) fetchChecksum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && filepath.Base(strings.TrimPrefix(fields[1], "*")) == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

// DownloadAndInstall downloads the installer, verifies its SHA256 (if known),
// launches it silently, and quits the app so toru.exe is unlocked for overwrite.
// The silent installer then relaunches the freshly-updated Toru (project.nsi).
//
// A guard dedupes concurrent installs (startup AutoUpdate racing a manual check):
// the first caller commits, later callers no-op. The guard is released on any
// pre-launch failure so a subsequent retry can proceed.
func (s *UpdateService) DownloadAndInstall(info UpdateInfo) error {
	if !s.installing.CompareAndSwap(false, true) {
		return nil // an install is already in flight
	}
	committed := false
	defer func() {
		if !committed {
			s.installing.Store(false)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dir := filepath.Join(os.TempDir(), "toru-update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, info.AssetName)
	if err := s.download(ctx, info.AssetURL, dst); err != nil {
		return err
	}

	if info.SHA256 != "" {
		got, err := sha256File(dst)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, info.SHA256) {
			_ = os.Remove(dst)
			return fmt.Errorf("update checksum mismatch: got %s, want %s", got, info.SHA256)
		}
	}

	// Per-user installer, silent. Start detached so it survives our exit.
	cmd := exec.Command(dst, "/S")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch installer: %w", err)
	}
	committed = true // installer is running; we are now committed to quitting

	if s.app != nil {
		// Best-effort: let an open window show "Updating…" instead of just
		// vanishing. Then release the executable lock so NSIS can overwrite
		// toru.exe; the installer relaunches the updated app afterward.
		s.app.Event.Emit(EventUpdateInstalling, info.Version)
		s.app.Quit()
	}
	return nil
}

func (s *UpdateService) download(ctx context.Context, url, dst string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(f, resp.Body)
	return err
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isNewer reports whether tag (vX.Y.Z) is newer than current. A "dev"/empty
// current never reports an update (avoids nagging in `wails3 dev`).
func isNewer(tag, current string) bool {
	if current == "" || current == "dev" {
		return false
	}
	return cmpSemver(strings.TrimPrefix(tag, "v"), strings.TrimPrefix(current, "v")) > 0
}

func cmpSemver(a, b string) int {
	na, prea := parseVer(a)
	nb, preb := parseVer(b)
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			if na[i] > nb[i] {
				return 1
			}
			return -1
		}
	}
	// Same X.Y.Z: a final release (no prerelease) outranks a prerelease.
	switch {
	case prea == "" && preb == "":
		return 0
	case prea == "": // a is final, b is prerelease
		return 1
	case preb == "": // b is final, a is prerelease
		return -1
	default:
		return strings.Compare(prea, preb)
	}
}

// parseVer returns the numeric [major,minor,patch] and the prerelease string
// (the part after '-', build metadata after '+' dropped).
func parseVer(v string) ([3]int, string) {
	v = strings.SplitN(v, "+", 2)[0] // drop build metadata
	num, pre, _ := strings.Cut(v, "-")
	var out [3]int
	for i, p := range strings.SplitN(num, ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out[i] = n
	}
	return out, pre
}
