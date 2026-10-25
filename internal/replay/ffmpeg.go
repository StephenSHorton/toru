package replay

import (
	"context"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// EventFFmpeg reports FFmpeg setup progress. Payload: FFmpegState.
const EventFFmpeg = "ffmpeg:state"

// FFmpegState is the setup state of the FFmpeg every recording runs through.
// The installer doesn't ship it; when none is found the app downloads it once.
type FFmpegState struct {
	Ready       bool   `json:"ready"`
	Downloading bool   `json:"downloading"`
	Percent     int    `json:"percent"`
	SizeMB      int    `json:"sizeMB"`
	Error       string `json:"error"`
}

// GetFFmpegState returns the current FFmpeg setup state.
func (s *ReplayService) GetFFmpegState() FFmpegState {
	s.ffMu.Lock()
	defer s.ffMu.Unlock()
	st := s.ff
	if !st.Downloading {
		_, err := capture.LocateFFmpeg()
		st.Ready = err == nil
	}
	st.SizeMB = capture.FFmpegDownloadBytes >> 20
	return st
}

// DownloadFFmpeg starts the one-time FFmpeg download when none is installed
// (the Settings retry button). It returns at once; progress arrives as
// EventFFmpeg, and recording starts by itself when the download lands.
func (s *ReplayService) DownloadFFmpeg() {
	if _, err := capture.LocateFFmpeg(); err == nil {
		return
	}
	s.ffMu.Lock()
	if s.ff.Downloading {
		s.ffMu.Unlock()
		return
	}
	s.ff = FFmpegState{Downloading: true}
	s.ffMu.Unlock()
	s.emitFFmpeg()
	go s.runFFmpegDownload()
}

func (s *ReplayService) runFFmpegDownload() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	lastPct := -1
	err := capture.DownloadFFmpeg(ctx, func(done, total int64) {
		pct := int(done * 100 / total)
		if pct == lastPct {
			return
		}
		lastPct = pct
		s.ffMu.Lock()
		s.ff.Percent = pct
		s.ffMu.Unlock()
		s.emitFFmpeg()
	})
	s.ffMu.Lock()
	if err != nil {
		s.ff = FFmpegState{Error: "Couldn't download the video components: " + err.Error()}
	} else {
		s.ff = FFmpegState{Ready: true, Percent: 100}
	}
	s.ffMu.Unlock()
	s.emitFFmpeg()
	if err == nil {
		// Whatever failed to start for want of FFmpeg starts now.
		_ = s.reconcile()
		s.notify(s.GetSettings())
	}
}

func (s *ReplayService) emitFFmpeg() { emit(EventFFmpeg, s.GetFFmpegState()) }
