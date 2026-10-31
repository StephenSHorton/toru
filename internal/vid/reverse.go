package vid

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// reverse.go is the reverse proxy (docs/video-editor-v3.md, Reverse): a
// clip's source range rendered backwards once, so the preview and the
// export can play it forwards. ffmpeg's reverse filter holds its whole
// input in memory (about 1 GB a second at 3440x1440 144 fps), so the range
// is reversed in 2 s chunks, each encoded all-intra (cheap frame-accurate
// seeks), and the chunks joined last-first. Proxies live in
// %LOCALAPPDATA%/toru/cache/reverse, trimmed oldest-used first to
// ReverseCacheLimit and cleared from Settings → Storage.

// EventReverseProgress carries a ReverseProgress while a proxy renders.
const EventReverseProgress = "reverse:progress"

// EventReverseCleared is sent when the cache is cleared, so editor windows
// drop the proxies they were showing.
const EventReverseCleared = "reverse:cleared"

// ReverseProgress is how far a reverse proxy is: Key is ReverseKey's value
// for the clip's range, Pct 0..1.
type ReverseProgress struct {
	Key string  `json:"key"`
	Pct float64 `json:"pct"`
}

// ReverseCacheLimit is how much the reverse cache keeps (bytes).
const ReverseCacheLimit int64 = 10 << 30

const reverseChunkMs = 2000

// reverseCacheDir is where proxies live (a var for tests).
var reverseCacheDir = func() string {
	base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "toru", "cache", "reverse")
}

// reverseKey names a range's proxy: the source's path, size and mtime (an
// edited source gets a fresh proxy) and the range.
func reverseKey(path string, inMs, outMs int) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d|%d", strings.ToLower(abs), fi.Size(), fi.ModTime().UnixNano(), inMs, outMs)))
	return hex.EncodeToString(h[:]), nil
}

// reverseRuns makes one render per key at a time (a second ask waits for
// the first), and one render at all: each is heavy on the CPU and GPU.
var reverseRuns = struct {
	mu   sync.Mutex
	busy map[string]*reverseRun
	one  sync.Mutex
}{busy: map[string]*reverseRun{}}

type reverseRun struct {
	done chan struct{}
	path string
	err  error
}

// ReverseKey is the key progress events use for a range (the frontend
// matches events to clips with it).
func (s *VideoService) ReverseKey(path string, inMs, outMs int) (string, error) {
	return reverseKey(path, inMs, outMs)
}

// ReverseProxy returns the reverse proxy of path's inMs..outMs, rendering
// it first if it isn't cached ("reverse:progress" events meanwhile). The
// proxy's time t shows the source at outMs - t.
func (s *VideoService) ReverseProxy(path string, inMs, outMs int) (string, error) {
	return reverseProxy(context.Background(), path, inMs, outMs, emitReverseProgress)
}

// ReverseCacheSize is how many bytes the reverse cache holds.
func (s *VideoService) ReverseCacheSize() (int64, error) {
	var total int64
	for _, f := range reverseCacheFiles() {
		total += f.Size()
	}
	return total, nil
}

// ClearReverseCache deletes every cached reverse proxy (not one that's
// rendering). Reversed clips render again when next shown or exported.
func (s *VideoService) ClearReverseCache() error {
	dir := reverseCacheDir()
	var errs []error
	for _, f := range reverseCacheFiles() {
		if err := os.Remove(filepath.Join(dir, f.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if app := application.Get(); app != nil {
		app.Event.Emit(EventReverseCleared, nil)
	}
	if len(errs) > 0 {
		return fmt.Errorf("some reversed clips are still open in the editor: %w", errors.Join(errs...))
	}
	return nil
}

func emitReverseProgress(key string, pct float64) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventReverseProgress, ReverseProgress{Key: key, Pct: pct})
	}
}

func reverseProxy(ctx context.Context, path string, inMs, outMs int, onProgress func(key string, pct float64)) (string, error) {
	if outMs <= inMs || inMs < 0 {
		return "", fmt.Errorf("reverse: bad range %d..%d ms", inMs, outMs)
	}
	key, err := reverseKey(path, inMs, outMs)
	if err != nil {
		return "", fmt.Errorf("reverse: %w", err)
	}
	dir := reverseCacheDir()
	out := filepath.Join(dir, key+".mp4")

	reverseRuns.mu.Lock()
	if _, err := os.Stat(out); err == nil {
		reverseRuns.mu.Unlock()
		now := time.Now()
		_ = os.Chtimes(out, now, now) // used again: newest for the LRU trim
		return out, nil
	}
	if run, ok := reverseRuns.busy[key]; ok {
		reverseRuns.mu.Unlock()
		select {
		case <-run.done:
			return run.path, run.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	run := &reverseRun{done: make(chan struct{})}
	reverseRuns.busy[key] = run
	reverseRuns.mu.Unlock()

	run.path, run.err = renderReverse(ctx, path, inMs, outMs, dir, out, func(p float64) {
		if onProgress != nil {
			onProgress(key, p)
		}
	})
	reverseRuns.mu.Lock()
	delete(reverseRuns.busy, key)
	reverseRuns.mu.Unlock()
	close(run.done)
	if run.err == nil {
		trimReverseCache(ReverseCacheLimit, out)
	}
	return run.path, run.err
}

// renderReverse writes the proxy: each 2 s chunk reversed (video and every
// audio stream) to an all-intra MKV with PCM audio, then the chunks joined
// last-first into one MP4 (video copied, audio encoded once, so there are
// no AAC gaps at the joins).
func renderReverse(ctx context.Context, path string, inMs, outMs int, dir, out string, progress func(float64)) (string, error) {
	reverseRuns.one.Lock()
	defer reverseRuns.one.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("reverse: %w", err)
	}
	work, err := os.MkdirTemp(dir, "part-")
	if err != nil {
		return "", fmt.Errorf("reverse: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	enc := capture.TrimH264Encoder()
	total := outMs - inMs
	n := (total + reverseChunkMs - 1) / reverseChunkMs
	var list strings.Builder
	for i := n - 1; i >= 0; i-- {
		// Chunk i covers in+i*2s..; listed last-first.
		fmt.Fprintf(&list, "file '%s'\n", filepath.ToSlash(filepath.Join(work, fmt.Sprintf("c%04d.mkv", i))))
	}
	for i := 0; i < n; i++ {
		start := inMs + i*reverseChunkMs
		dur := min(reverseChunkMs, outMs-start)
		chunk := filepath.Join(work, fmt.Sprintf("c%04d.mkv", i))
		args := reverseChunkArgs(path, start, dur, enc, chunk)
		done := float64(i) / float64(n+1)
		if err := capture.RunFFmpegProgress(ctx, args, dur, func(p float64) { progress(done + p/float64(n+1)) }); err != nil {
			return "", fmt.Errorf("reverse %s: %w", filepath.Base(path), err)
		}
		progress(float64(i+1) / float64(n+1))
	}
	listFile := filepath.Join(work, "chunks.txt")
	if err := os.WriteFile(listFile, []byte(list.String()), 0o644); err != nil {
		return "", fmt.Errorf("reverse: %w", err)
	}
	tmp := filepath.Join(work, "proxy.mp4")
	join := []string{"-y", "-f", "concat", "-safe", "0", "-i", listFile, "-map", "0:v:0", "-map", "0:a?", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", tmp}
	if err := capture.RunFFmpegProgress(ctx, join, total, func(p float64) { progress(float64(n)/float64(n+1) + p/float64(n+1)) }); err != nil {
		return "", fmt.Errorf("reverse %s: join: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", fmt.Errorf("reverse: %w", err)
	}
	progress(1)
	return out, nil
}

// reverseChunkArgs reverses durMs of path from startMs: video all-intra
// (every frame a keyframe), every audio stream reversed as PCM.
func reverseChunkArgs(path string, startMs, durMs int, enc, out string) []string {
	args := []string{"-y", "-ss", msSeconds(startMs), "-t", msSeconds(durMs), "-i", path,
		"-map", "0:v:0", "-map", "0:a?", "-vf", "reverse", "-af", "areverse"}
	args = append(args, capture.TrimH264Args(enc)...)
	return append(args, "-g", "1", "-bf", "0", "-pix_fmt", "yuv420p", "-c:a", "pcm_s16le", "-f", "matroska", out)
}

// reverseCacheFiles lists finished proxies (not work folders).
func reverseCacheFiles() []os.FileInfo {
	entries, err := os.ReadDir(reverseCacheDir())
	if err != nil {
		return nil
	}
	var out []os.FileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mp4") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			out = append(out, fi)
		}
	}
	return out
}

// trimReverseCache deletes the least recently used proxies until the cache
// fits limit. keep (the proxy just made) always stays.
func trimReverseCache(limit int64, keep string) {
	files := reverseCacheFiles()
	var total int64
	for _, f := range files {
		total += f.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime().Before(files[j].ModTime()) })
	dir := reverseCacheDir()
	for _, f := range files {
		if total <= limit {
			return
		}
		full := filepath.Join(dir, f.Name())
		if strings.EqualFold(full, keep) {
			continue
		}
		if os.Remove(full) == nil {
			total -= f.Size()
		}
	}
}

// withReverseProxies is the export's view of reversed clips: each plays its
// proxy forwards (rendering it first if needed), as its own media with the
// range 0..length. The speed curve already runs over the reversed footage.
func withReverseProxies(ctx context.Context, req ProjectExport) (ProjectExport, error) {
	p := req.Project
	p.Media = append([]MediaJSON(nil), p.Media...) // don't grow the caller's slice
	media := map[string]MediaJSON{}
	for _, m := range p.Media {
		media[m.ID] = m
	}
	tracks := make([]TrackJSON, len(p.Tracks))
	changed := false
	for ti, t := range p.Tracks {
		tracks[ti] = t
		clips := make([]ClipJSON, len(t.Clips))
		copy(clips, t.Clips)
		for ci, c := range clips {
			m := media[c.Media]
			if !c.Reversed || c.Hold || m.Kind != "video" {
				continue
			}
			proxy, err := reverseProxy(ctx, m.Path, c.In, c.Out, emitReverseProgress)
			if err != nil {
				return req, err
			}
			pm := m
			pm.ID = "reverse:" + c.ID
			pm.Path = proxy
			pm.Duration = c.Out - c.In
			p.Media = append(p.Media, pm)
			c.Media, c.Out, c.In, c.Reversed = pm.ID, c.Out-c.In, 0, false
			clips[ci] = c
			changed = true
		}
		tracks[ti].Clips = clips
	}
	if !changed {
		return req, nil
	}
	p.Tracks = tracks
	req.Project = p
	return req, nil
}
