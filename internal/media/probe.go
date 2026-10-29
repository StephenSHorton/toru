package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // DecodeConfig formats for images
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/vid"
)

const probeTimeout = 15 * time.Second

// probeFile turns a path into Media: images by their header (ffprobe for
// formats Go can't read, like WebP), video and audio by ffprobe.
func probeFile(path string) (Media, error) {
	kind := kindByExt(path)
	if kind == "" {
		return Media{}, fmt.Errorf("%s: not a video, audio or image file", path)
	}
	abs, _, err := absFile(path)
	if err != nil {
		return Media{}, err
	}
	m := Media{ID: mediaID(abs), Path: abs, Kind: kind, Tracks: []vid.AudioTrack{}}
	m.URL = mediaURL(m.ID, abs)

	if kind == KindImage {
		if w, h, err := imageSize(abs); err == nil {
			m.Width, m.Height = w, h
			return m, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	raw, err := ffprobeJSON(ctx, abs)
	if err != nil {
		return Media{}, err
	}
	p, err := parseProbe(raw)
	if err != nil {
		return Media{}, fmt.Errorf("%s: %w", abs, err)
	}
	if kind == KindImage {
		if p.width <= 0 {
			return Media{}, fmt.Errorf("%s: no picture", abs)
		}
		m.Width, m.Height = p.width, p.height
		return m, nil
	}
	switch {
	case kind == KindVideo && p.width > 0:
		m.Width, m.Height, m.FPS = p.width, p.height, p.fps
	case len(p.tracks) > 0:
		m.Kind = KindAudio // audio file, or a video container with no picture
	default:
		return Media{}, fmt.Errorf("%s: no video or audio", abs)
	}
	m.Duration = p.duration
	m.Tracks = p.tracks
	m.HasAudio = len(p.tracks) > 0
	return m, nil
}

func imageSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, errors.New("empty image")
	}
	return cfg.Width, cfg.Height, nil
}

func ffprobeJSON(ctx context.Context, path string) ([]byte, error) {
	bin, err := capture.LocateFFprobe()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error",
		"-show_entries", "format=duration:stream=codec_type,width,height,avg_frame_rate,r_frame_rate,duration:stream_tags=title,name,handler_name:stream_disposition=default,attached_pic",
		"-of", "json", path)
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", path, err)
	}
	return out, nil
}

// probed is what parseProbe reads out of ffprobe's JSON.
type probed struct {
	width, height int
	fps           float64
	duration      float64 // seconds
	tracks        []vid.AudioTrack
}

type probeJSON struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		Duration     string `json:"duration"`
		Disposition  struct {
			Default     int `json:"default"`
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		Tags struct {
			Title       string `json:"title"`
			Name        string `json:"name"`
			HandlerName string `json:"handler_name"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// parseProbe reads the first real video stream (cover art in an MP3 is not
// one) and the audio streams. Track titles and the mix flag follow the same
// rules as vid.VideoService.ProbeMedia, so per-source-track levels line up.
func parseProbe(raw []byte) (probed, error) {
	var p probeJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		return probed{}, fmt.Errorf("probe: %w", err)
	}
	out := probed{tracks: []vid.AudioTrack{}}
	streamDur := 0.0
	mix := -1
	for _, st := range p.Streams {
		switch st.CodecType {
		case "video":
			if out.width > 0 || st.Disposition.AttachedPic == 1 || st.Width <= 0 {
				continue
			}
			out.width, out.height = st.Width, st.Height
			out.fps = parseRate(st.AvgFrameRate)
			if out.fps <= 0 {
				out.fps = parseRate(st.RFrameRate)
			}
			streamDur, _ = strconv.ParseFloat(st.Duration, 64)
		case "audio":
			idx := len(out.tracks)
			out.tracks = append(out.tracks, vid.AudioTrack{Index: idx, Title: trackTitle(st.Tags.Title, st.Tags.Name, st.Tags.HandlerName)})
			if mix < 0 && st.Disposition.Default == 1 {
				mix = idx
			}
			if streamDur <= 0 {
				streamDur, _ = strconv.ParseFloat(st.Duration, 64)
			}
		}
	}
	if len(out.tracks) > 1 {
		out.tracks[max(mix, 0)].IsMix = true
	}
	out.duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	if out.duration <= 0 {
		out.duration = streamDur
	}
	return out, nil
}

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
