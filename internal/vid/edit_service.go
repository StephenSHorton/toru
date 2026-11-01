package vid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// edit_service.go runs the video editor's exports (docs/video-editor.md):
// probing sources, the Save As dialog, and the ffmpeg run with progress and
// cancel. The args themselves come from BuildEditArgs (edit_args.go).

// EventEditProgress carries an export's progress to the editor window as
// {pct} in 0..1.
const EventEditProgress = "edit:progress"

// EditProgress is the "edit:progress" payload.
type EditProgress struct {
	Pct float64 `json:"pct"`
}

// ErrExportCancelled is what Export returns after CancelExport.
var ErrExportCancelled = errors.New("export cancelled")

// editState is the one export that can run at a time (one editor window
// exporting is the normal case; a second Export waits for none and fails).
type editState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// editRun is package state rather than a VideoService field so VideoService
// stays a plain struct for its existing tests and New().
var editRun editState

// probeTimeout bounds one ffprobe call.
const probeTimeout = 15 * time.Second

// ProbeMedia reports a source's duration, frame size, frame rate and audio
// tracks (with their titles, and which one is the mix when a clip has
// separate tracks).
func (s *VideoService) ProbeMedia(path string) (MediaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return probeMedia(ctx, path)
}

func probeMedia(ctx context.Context, path string) (MediaInfo, error) {
	if _, err := os.Stat(path); err != nil {
		return MediaInfo{}, fmt.Errorf("probe: %w", err)
	}
	bin, err := capture.LocateFFprobe()
	if err != nil {
		return MediaInfo{}, err
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error",
		"-show_entries", "format=duration:stream=index,codec_type,width,height,color_space,avg_frame_rate,r_frame_rate,duration:stream_tags=title,name,handler_name:stream_disposition=default",
		"-of", "json", path)
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("probe %s: %w", filepath.Base(path), err)
	}
	return parseProbe(out)
}

// probeJSON is the part of ffprobe's -of json output parseProbe reads.
type probeJSON struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		ColorSpace   string `json:"color_space"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		Duration     string `json:"duration"`
		Disposition  struct {
			Default int `json:"default"`
		} `json:"disposition"`
		Tags struct {
			Title       string `json:"title"`
			Name        string `json:"name"` // newer ffmpeg reads an MP4 track title back as "name"
			HandlerName string `json:"handler_name"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parseProbe turns ffprobe JSON into MediaInfo. Audio tracks are numbered
// in stream order among audio streams (the "a:N" index ffmpeg maps by).
// With several tracks, the mix is the default-disposition one, else the
// first: Toru's separate-tracks clips put "All audio" first as the default.
// A single track is just the audio, not a mix.
func parseProbe(raw []byte) (MediaInfo, error) {
	var p probeJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return MediaInfo{}, fmt.Errorf("probe: %w", err)
	}
	var info MediaInfo
	videoDur := 0.0
	mix := -1
	for _, st := range p.Streams {
		switch st.CodecType {
		case "video":
			if info.Width > 0 {
				continue // first video stream only (skips cover art after it)
			}
			info.Width, info.Height = st.Width, st.Height
			if cs := strings.ToLower(strings.TrimSpace(st.ColorSpace)); cs != "unknown" {
				info.ColorSpace = cs
			}
			info.FPS = parseRate(st.AvgFrameRate)
			if info.FPS <= 0 {
				info.FPS = parseRate(st.RFrameRate)
			}
			videoDur, _ = strconv.ParseFloat(st.Duration, 64)
		case "audio":
			idx := len(info.Tracks)
			info.Tracks = append(info.Tracks, AudioTrack{Index: idx, Title: trackTitle(st.Tags.Title, st.Tags.Name, st.Tags.HandlerName)})
			if mix < 0 && st.Disposition.Default == 1 {
				mix = idx
			}
		}
	}
	if info.Width <= 0 || info.Height <= 0 {
		return MediaInfo{}, errors.New("probe: no video stream")
	}
	info.HasAudio = len(info.Tracks) > 0
	if len(info.Tracks) > 1 {
		if mix < 0 {
			mix = 0
		}
		info.Tracks[mix].IsMix = true
	}
	dur, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if dur <= 0 {
		dur = videoDur
	}
	info.DurationMs = int(dur * 1000)
	return info, nil
}

// trackTitle prefers the stream title (title, or name as newer ffmpeg reads
// it from MP4), then a handler name that isn't one of the muxers' generic
// defaults.
func trackTitle(title, name, handler string) string {
	for _, t := range []string{title, name} {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	h := strings.TrimSpace(handler)
	switch strings.ToLower(h) {
	case "", "soundhandler", "sound media handler", "core media audio", "isomedia file produced by google", "l-smash audio handler":
		return ""
	}
	return h
}

// parseRate reads an ffprobe rate like "60/1" or "30000/1001" ("0/0" = 0).
func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	if !ok {
		f, _ := strconv.ParseFloat(r, 64)
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}

// Export renders req to req.OutPath and returns the written path, emitting
// "edit:progress" as it goes. It writes to a temp file beside the output and
// renames it into place at the end, so a failed or cancelled export leaves
// nothing behind. The Discord preset re-encodes the result to fit 10MB; the
// returned path then ends in .webm unless the edit was already small enough.
func (s *VideoService) Export(req EditRequest) (string, error) {
	return runExclusive(func(ctx context.Context) (string, error) {
		return s.export(ctx, req, emitEditProgress)
	})
}

// runExclusive runs one export at a time (v1 Export or v2 ExportProject)
// under a context CancelExport cancels, and turns a cancelled run into
// ErrExportCancelled.
func runExclusive(run func(ctx context.Context) (string, error)) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	editRun.mu.Lock()
	if editRun.cancel != nil {
		editRun.mu.Unlock()
		return "", errors.New("an export is already running")
	}
	editRun.cancel = cancel
	editRun.mu.Unlock()
	defer func() {
		editRun.mu.Lock()
		editRun.cancel = nil
		editRun.mu.Unlock()
	}()

	out, err := run(ctx)
	if ctx.Err() != nil {
		return "", ErrExportCancelled
	}
	return out, err
}

// ExportProject renders a v2 project (docs/video-editor-v2.md) to
// req.OutPath like Export: "edit:progress" events, CancelExport, a temp file
// renamed into place, the Discord preset chained.
func (s *VideoService) ExportProject(req ProjectExport) (string, error) {
	return runExclusive(func(ctx context.Context) (string, error) {
		return s.exportProject(ctx, req, emitEditProgress)
	})
}

func (s *VideoService) exportProject(ctx context.Context, req ProjectExport, onProgress func(float64)) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}
	for _, m := range req.Project.Media {
		if _, err := os.Stat(m.Path); err != nil {
			return "", fmt.Errorf("export: %s is missing (moved or deleted?)", m.Path)
		}
	}
	markUntagged709(ctx, &req.Project)
	if o := req.Options; o != nil && o.Encoder != "" && !encoderUsable(o.Encoder) {
		return "", fmt.Errorf("export: the %s encoder doesn't work on this PC", o.Encoder)
	}
	// Reversed clips play their reverse proxy (reverse.go), made now if needed.
	req, err := withReverseProxies(ctx, req)
	if err != nil {
		return "", err
	}
	work, err := os.MkdirTemp("", "toru-editfx-")
	if err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	// The editor's text images, written into work (project_text.go).
	// Audio-only exports draw no picture.
	textFilter := ""
	if o := req.Options; o == nil || (o.Format != FormatM4A && o.Format != FormatWAV) {
		if textFilter, err = ProjectTextFilter(req.Project, work); err != nil {
			return "", fmt.Errorf("export: %w", err)
		}
	}
	// Build once up front, so a failure (writing a LUT) is an error, not a
	// broken ffmpeg run.
	if _, err := BuildProjectArgs(req, capture.TrimH264Encoder(), textFilter, work); err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	build := func(tmp string) []string {
		run := req
		run.OutPath = tmp
		args, _ := BuildProjectArgs(run, capture.TrimH264Encoder(), textFilter, work)
		return args
	}
	return s.render(ctx, req.OutPath, req.Preset == "discord", build, req.ExportLengthMs(), onProgress)
}

// untaggedHDLines is where an untagged video counts as BT.709: phone and
// other apps' HD clips are usually BT.709 without saying so; SD stays
// BT.601 (ffmpeg's default). The preview uses the same rule
// (frontend/src/trim/colorspace.ts).
const untaggedHDLines = 720

// untagged709Filter tags frames BT.709, so every conversion after it (and
// the output) reads them that way.
const untagged709Filter = "setparams=colorspace=bt709:color_primaries=bt709:color_trc=bt709"

// markUntagged709 probes each video source and sets Untagged709 on the
// untagged HD ones. A failed probe leaves the source as it is.
func markUntagged709(ctx context.Context, p *ProjectJSON) {
	p.Media = append([]MediaJSON(nil), p.Media...) // don't change the caller's slice
	for i, m := range p.Media {
		if m.Kind != "video" || m.Height < untaggedHDLines {
			continue
		}
		if info, err := probeMedia(ctx, m.Path); err == nil && info.ColorSpace == "" {
			p.Media[i].Untagged709 = true
		}
	}
}

// CancelExport stops the running export, if any. Export then returns
// ErrExportCancelled and removes its partial output.
func (s *VideoService) CancelExport() error {
	editRun.mu.Lock()
	defer editRun.mu.Unlock()
	if editRun.cancel != nil {
		editRun.cancel()
	}
	return nil
}

// export does Export's work under ctx, reporting progress to onProgress.
func (s *VideoService) export(ctx context.Context, req EditRequest, onProgress func(float64)) (string, error) {
	info := make([]MediaInfo, len(req.Sources))
	for i, src := range req.Sources {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		m, err := probeMedia(pctx, src)
		cancel()
		if err != nil {
			return "", fmt.Errorf("export: %w", err)
		}
		info[i] = m
	}
	if err := req.Validate(info); err != nil {
		return "", err
	}
	textFilter, cleanupText, err := editTextFilter(req, info)
	if err != nil {
		return "", err
	}
	defer cleanupText()
	build := func(tmp string) []string {
		run := req
		run.OutPath = tmp
		return BuildEditArgs(run, info, capture.TrimH264Encoder(), textFilter)
	}
	return s.render(ctx, req.OutPath, req.Preset == "discord", build, EditDurationMs(req, info), onProgress)
}

// render runs one export's ffmpeg pass (args from build, writing the temp
// file it is given) with progress over totalMs, chains the Discord re-encode
// when discord is set, and renames the result to outPath (its extension
// following the real container). A failed or cancelled run leaves nothing
// behind.
func (s *VideoService) render(ctx context.Context, outPath string, discord bool, build func(tmp string) []string, totalMs int, onProgress func(float64)) (string, error) {
	if dir := filepath.Dir(outPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("export: %w", err)
		}
	}

	// The edit pass is all of the progress bar, or the first half when the
	// Discord re-encode follows it.
	scale := 1.0
	if discord {
		scale = 0.5
	}
	report := func(p float64) {
		if onProgress != nil {
			onProgress(p * scale)
		}
	}

	tmp := partPath(outPath)
	report(0)
	if err := capture.RunFFmpegProgress(ctx, build(tmp), totalMs, report); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("export: %w", err)
	}

	final := tmp
	if discord {
		report(1)
		d, err := s.exportForDiscord(ctx, tmp)
		if err != nil {
			// Its output (still over the cap, or cut short) is ours to clean up.
			_ = os.Remove(strings.TrimSuffix(tmp, filepath.Ext(tmp)) + "-discord.webm")
			_ = os.Remove(tmp)
			return "", err
		}
		if d != tmp {
			_ = os.Remove(tmp)
			final = d
		}
	}
	// Discord may change the container (.webm); keep the user's name with
	// the real extension.
	dst := outPath
	if ext := filepath.Ext(final); !strings.EqualFold(ext, filepath.Ext(dst)) {
		dst = strings.TrimSuffix(dst, filepath.Ext(dst)) + ext
	}
	if ctx.Err() != nil {
		_ = os.Remove(final)
		return "", ErrExportCancelled
	}
	if err := os.Rename(final, dst); err != nil {
		_ = os.Remove(final)
		return "", fmt.Errorf("export: %w", err)
	}
	if onProgress != nil {
		onProgress(1)
	}
	return dst, nil
}

// partPath is the temp file an export writes before renaming it to out: in
// the same folder (so the rename is a move, not a copy) with the same
// extension (ffmpeg picks the container from it).
func partPath(out string) string {
	ext := filepath.Ext(out)
	return strings.TrimSuffix(out, ext) + fmt.Sprintf(".toru-part-%d", time.Now().UnixNano()) + ext
}

// editTextFilter builds the text overlay filter for BuildEditArgs with
// TextFilter, laid out on the output frame before the preset's scale. The
// text files go in their own temp dir; cleanup removes it and must run after
// ffmpeg, whether it succeeded or not.
func editTextFilter(req EditRequest, info []MediaInfo) (filter string, cleanup func(), err error) {
	if len(req.Texts) == 0 {
		return "", func() {}, nil
	}
	dir, err := os.MkdirTemp("", "toru-edittext-")
	if err != nil {
		return "", nil, fmt.Errorf("export: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	w, h := EditFrameSize(req, info)
	filter, _, err = TextFilter(req.Texts, w, h, DefaultTextFont, dir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("export: %w", err)
	}
	return filter, cleanup, nil
}

func emitEditProgress(p float64) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventEditProgress, EditProgress{Pct: p})
	}
}

// PickExportPath shows the Save As dialog for an export of sourcePath (the
// first source), suggesting <name>-edited.mp4 in its folder. It returns the
// chosen path, or "" when the dialog was cancelled.
func (s *VideoService) PickExportPath(sourcePath string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("save as: no running application")
	}
	dlg := app.Dialog.SaveFile().
		SetFilename(editedName(sourcePath)).
		AddFilter("MP4 Video", "*.mp4").
		AddFilter("All Files", "*.*")
	if dir := filepath.Dir(sourcePath); sourcePath != "" && dir != "" {
		dlg.SetDirectory(dir)
	}
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		return "", nil // cancelled (the dialog reports cancel as an error)
	}
	if filepath.Ext(chosen) == "" {
		chosen += ".mp4"
	}
	return chosen, nil
}

// PickAudioExportPath shows the Save As dialog for a clip's sound (Save
// audio as file), suggesting <name>-audio.m4a in sourcePath's folder, with
// M4A and WAV to choose from. It returns the chosen path, ending in .m4a or
// .wav (anything else gets .m4a added), or "" when cancelled.
func (s *VideoService) PickAudioExportPath(sourcePath string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("save as: no running application")
	}
	dlg := app.Dialog.SaveFile().
		SetFilename(audioName(sourcePath)).
		AddFilter("M4A Audio", "*.m4a").
		AddFilter("WAV Audio", "*.wav")
	if dir := filepath.Dir(sourcePath); sourcePath != "" && dir != "" {
		dlg.SetDirectory(dir)
	}
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		return "", nil // cancelled (the dialog reports cancel as an error)
	}
	return audioPath(chosen), nil
}

// audioName is the Save As suggestion for the sound of src.
func audioName(src string) string {
	base := filepath.Base(src)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "audio.m4a"
	}
	return name + "-audio.m4a"
}

// audioPath makes a chosen path end in .m4a or .wav.
func audioPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".m4a", ".wav":
		return p
	}
	return p + ".m4a"
}

// editedName is the Save As suggestion for an export of src.
func editedName(src string) string {
	base := filepath.Base(src)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "video"
	}
	return name + "-edited.mp4"
}
