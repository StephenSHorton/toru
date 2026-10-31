// Package update is Toru's in-app updater: Check for Updates + a background
// download that announces itself and lets the user pick when to restart.
//
// It queries the GitHub Releases API for the latest release, compares the tag
// against the running version (injected via -ldflags -X main.version), finds the
// Windows NSIS installer asset, downloads it, verifies it against the release's
// SHA256SUMS, and then parks in the "ready" phase. From there the user either
// restarts right away (RestartToUpdate: silent installer + quit; the installer
// relaunches Toru) or keeps working, in which case the installer runs when Toru
// quits (InstallOnExit: silent installer with /NORELAUNCH). Nothing installs or
// quits behind the user's back.
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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventUpdateStatus is emitted (payload: Status) on every phase transition so
// an open window can show the download, the "ready" banner, or the restart.
const EventUpdateStatus = "update:status"

// Update phases, in order of a normal run. "error" can follow any of the first
// three; "idle" also means "up to date" after a check that found nothing.
const (
	PhaseIdle        = "idle"
	PhaseChecking    = "checking"
	PhaseDownloading = "downloading"
	PhaseReady       = "ready"
	PhaseInstalling  = "installing"
	PhaseError       = "error"
)

// Status is the updater's state as the frontend and tray see it.
type Status struct {
	Phase   string `json:"phase"`   // idle|checking|downloading|ready|installing|error
	Version string `json:"version"` // target version once known, e.g. "1.3.0"
	Notes   string `json:"notes"`   // release body (markdown)
	// InstallerPath is the verified installer on disk once Phase is ready. It is
	// a local detail the JS side has no use for, so it is not exported.
	InstallerPath string `json:"-"`
	Error         string `json:"error"` // set while Phase is error
	// InstallFailed is set with an error that came from the installer (the
	// last silent install couldn't replace toru.exe), not from the check or
	// the download.
	InstallFailed bool `json:"installFailed"`
}

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
	repo    string // "owner/repo", e.g. "StephenSHorton/toru"
	current string // running version (ldflags-injected; "dev" in non-release builds)
	client  *http.Client
	app     *application.App

	mu       sync.Mutex
	status   Status
	onChange func(Status)
	inFlight atomic.Bool // one check+download at a time

	// launch starts the installer detached. Swapped for a fake in tests.
	launch func(path string, args ...string) error
}

// New returns an UpdateService for the given GitHub repo and running version.
func New(repo, currentVersion string) *UpdateService {
	return &UpdateService{
		repo:    repo,
		current: currentVersion,
		client:  &http.Client{Timeout: 30 * time.Second},
		status:  Status{Phase: PhaseIdle},
		launch: func(path string, args ...string) error {
			// Per-user installer, silent. Start detached so it survives our exit.
			return exec.Command(path, args...).Start()
		},
	}
}

// SetApp injects the running app (used to emit status and to quit before the
// installer runs).
func (s *UpdateService) SetApp(app *application.App) { s.app = app }

// SetOnChange registers a callback fired after every status transition. main
// uses it to rebuild the tray menu ("Restart to update" appears when ready).
//
//wails:ignore
func (s *UpdateService) SetOnChange(fn func(Status)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// GetVersion returns the running version string.
func (s *UpdateService) GetVersion() string { return s.current }

// GetStatus returns the current updater status.
func (s *UpdateService) GetStatus() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// setStatus mutates the status under the lock, then notifies listeners
// outside it (the event emitter and tray rebuild must never run under mu).
func (s *UpdateService) setStatus(mut func(st *Status)) Status {
	s.mu.Lock()
	mut(&s.status)
	st := s.status
	cb := s.onChange
	s.mu.Unlock()
	if s.app != nil {
		s.app.Event.Emit(EventUpdateStatus, st)
	}
	if cb != nil {
		cb(st)
	}
	return st
}

// checkEvery is how often a running Toru looks for a new release. Toru lives
// in the tray for days, so a check only at startup missed every release
// published while it was open. Two unauthenticated GitHub API calls an hour
// stay far under the 60/hour limit.
const checkEvery = 30 * time.Minute

// AutoUpdate runs on startup: it checks for a newer release and, if one exists,
// downloads and verifies it in the background, then announces that it is ready
// (status event, plus the tray item and notification the tray adds on ready). It never installs or quits on its own —
// the user restarts when they like, or the installer runs when Toru quits.
// After that first check it checks again every checkEvery for as long as Toru
// runs, until an update is ready.
//
// It runs as a fire-and-forget goroutine on startup. All errors are logged and
// swallowed — a failed or offline update check must never block startup.
//
// First it tidies up after the last update: it removes toru.exe.old files the
// installer left beside the exe, and if the last silent install failed it
// shows that error and waits a full checkEvery before trying again (Check for
// Updates retries at once).
func (s *UpdateService) AutoUpdate() {
	if exe, err := os.Executable(); err == nil {
		removeOldExes(exe)
	}
	failed := s.reportInstallFailure()
	autoUpdate(checkEvery, nil, failed, s.CheckAndDownload)
}

// autoUpdate is AutoUpdate's loop with the interval, a stop channel and the
// check itself passed in, so tests can run it without the network. With
// waitFirst it waits one interval before the first check.
func autoUpdate(every time.Duration, stop <-chan struct{}, waitFirst bool, check func() (Status, error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	if waitFirst {
		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
	for {
		st, err := check()
		if err != nil {
			log.Printf("toru: auto-update failed: %v", err)
		}
		if st.Phase == PhaseReady || st.Phase == PhaseInstalling {
			return // announced; nothing left to look for until Toru restarts
		}
		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
}

// CheckAndDownload is the manual "Check for Updates" path: check, download and
// verify synchronously, and return the final status. One run is in flight at a
// time; a concurrent caller (startup AutoUpdate racing a click) gets the current
// status back. Once an update is ready or installing there is nothing to do, so
// that status is returned as is.
func (s *UpdateService) CheckAndDownload() (Status, error) {
	if !s.inFlight.CompareAndSwap(false, true) {
		return s.GetStatus(), nil
	}
	defer s.inFlight.Store(false)

	if st := s.GetStatus(); st.Phase == PhaseReady || st.Phase == PhaseInstalling {
		return st, nil
	}

	s.setStatus(func(st *Status) { *st = Status{Phase: PhaseChecking} })
	info, err := s.CheckForUpdate()
	if err != nil {
		return s.fail(err), err
	}
	if info == nil {
		// Up to date (or a dev build).
		return s.setStatus(func(st *Status) { *st = Status{Phase: PhaseIdle} }), nil
	}

	log.Printf("toru: downloading update v%s", info.Version)
	s.setStatus(func(st *Status) {
		*st = Status{Phase: PhaseDownloading, Version: info.Version, Notes: info.Notes}
	})
	path, err := s.downloadVerified(*info)
	if err != nil {
		return s.fail(err), err
	}

	st := s.setStatus(func(st *Status) {
		st.Phase, st.InstallerPath, st.Error = PhaseReady, path, ""
	})
	log.Printf("toru: update v%s ready at %s", info.Version, path)
	// The ready transition is the announcement: the status event drives the
	// banner, and onChange rebuilds the tray, which adds "Restart to update" and
	// posts the "Toru vX is ready" notification (tray.go).
	return st, nil
}

// fail records an error status and logs it, keeping the version if known.
func (s *UpdateService) fail(err error) Status {
	log.Printf("toru: update: %v", err)
	return s.setStatus(func(st *Status) {
		st.Phase, st.Error, st.InstallerPath = PhaseError, err.Error(), ""
	})
}

// RestartToUpdate launches the ready installer silently and quits Toru so the
// exe is unlocked for overwrite. The installer waits for the exit, installs,
// and relaunches Toru (project.nsi).
func (s *UpdateService) RestartToUpdate() error {
	s.mu.Lock()
	if s.status.Phase != PhaseReady {
		st := s.status
		s.mu.Unlock()
		return fmt.Errorf("no update ready to install (phase %q)", st.Phase)
	}
	path := s.status.InstallerPath
	// Flip to installing BEFORE quitting: app.Quit runs OnShutdown, whose
	// InstallOnExit must see that an install is already under way.
	s.status.Phase = PhaseInstalling
	s.mu.Unlock()

	if err := s.launch(path, "/S"); err != nil {
		s.setStatus(func(st *Status) { st.Phase = PhaseReady })
		return fmt.Errorf("launch installer: %w", err)
	}
	log.Printf("toru: restarting to update (installer %s)", path)
	s.setStatus(func(*Status) {}) // emit the installing transition
	if s.app != nil {
		s.app.Quit()
	}
	return nil
}

// InstallOnExit is called from the app's OnShutdown. If an update is ready and
// nothing is installing, it launches the installer silently with /NORELAUNCH
// and returns immediately: the process exits, the installer waits for the exe
// lock, installs, and does NOT bring Toru back (the user chose to quit).
//
//wails:ignore
func (s *UpdateService) InstallOnExit() {
	s.mu.Lock()
	if s.status.Phase != PhaseReady {
		s.mu.Unlock()
		return
	}
	path := s.status.InstallerPath
	s.status.Phase = PhaseInstalling
	s.mu.Unlock()

	if err := s.launch(path, "/S", "/NORELAUNCH"); err != nil {
		log.Printf("toru: install on exit: launch installer: %v", err)
		return
	}
	log.Printf("toru: installing update on exit (installer %s)", path)
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

// downloadVerified downloads the installer into a new directory of the temp
// update folder (install.go, updateDir) and verifies its SHA256 (if known).
// It returns the installer path.
func (s *UpdateService) downloadVerified(info UpdateInfo) (string, error) {
	if info.AssetName == "" || info.AssetURL == "" {
		return "", errors.New("update has no installer asset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	root := updateDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	cleanDownloads(root)
	dir, err := os.MkdirTemp(root, "v"+info.Version+"-")
	if err != nil {
		return "", fmt.Errorf("make a folder for the installer: %w", err)
	}
	dst := filepath.Join(dir, filepath.Base(info.AssetName))
	if err := s.download(ctx, info.AssetURL, dst); err != nil {
		return "", err
	}

	if info.SHA256 != "" {
		got, err := sha256File(dst)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(got, info.SHA256) {
			_ = os.Remove(dst)
			return "", fmt.Errorf("update checksum mismatch: got %s, want %s", got, info.SHA256)
		}
	}
	return dst, nil
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
		return fmt.Errorf("save the installer: %w", err)
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
