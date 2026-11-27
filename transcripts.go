package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/toru/internal/transcript"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// transcripts.go is the Game Room page's Transcripts list: every call
// transcript saved on this PC (internal/transcript), to open, copy, export,
// rename and delete. The call's transcription itself runs in
// internal/stream (transcribe.go).
//
// JS binding name: TranscriptService.*
type TranscriptService struct {
	store *transcript.Store
	app   *application.App
	copy  func(text, what string) error
}

// TranscriptView is one transcript, opened.
type TranscriptView struct {
	Meta  transcript.Meta   `json:"meta"`
	Lines []transcript.Line `json:"lines"`
}

// List returns every transcript, newest first.
func (s *TranscriptService) List() ([]transcript.Meta, error) {
	return s.store.List()
}

// Open returns a transcript with all its lines, in time order.
func (s *TranscriptService) Open(id string) (TranscriptView, error) {
	m, lines, err := s.store.Read(id, transcript.Range{})
	if err != nil {
		return TranscriptView{}, err
	}
	return TranscriptView{Meta: m, Lines: lines}, nil
}

// Rename sets a transcript's title ("" goes back to the one made from the
// people and date).
func (s *TranscriptService) Rename(id, title string) (transcript.Meta, error) {
	return s.store.Rename(id, title)
}

// Delete removes a transcript (the page asks first).
func (s *TranscriptService) Delete(id string) error {
	return s.store.Delete(id)
}

// Copy puts a transcript on the clipboard as plain text.
func (s *TranscriptService) Copy(id string) error {
	text, err := s.store.Export(id, "txt")
	if err != nil {
		return err
	}
	return s.copy(text, "Transcript")
}

// Export saves a transcript as Markdown ("md") or plain text ("txt") to a
// file the person picks. It returns the path, or "" if they cancelled.
func (s *TranscriptService) Export(id, format string) (string, error) {
	text, err := s.store.Export(id, format)
	if err != nil {
		return "", err
	}
	m, err := s.store.Get(id)
	if err != nil {
		return "", err
	}
	if s.app == nil {
		return "", errors.New("no app for the save dialog")
	}
	ext, filter := ".txt", []string{"Text", "*.txt"}
	if format == "md" {
		ext, filter = ".md", []string{"Markdown", "*.md"}
	}
	dlg := s.app.Dialog.SaveFile().SetFilename(safeFileName(m.Title) + ext).AddFilter(filter[0], filter[1])
	if win := s.app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil // cancelled
	}
	if !strings.EqualFold(filepath.Ext(path), ext) {
		path += ext
	}
	return path, os.WriteFile(path, []byte(text), 0o644)
}

// safeFileName makes a title usable as a Windows file name.
func safeFileName(title string) string {
	var b strings.Builder
	for _, r := range title {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			r = '-'
		}
		b.WriteRune(r)
	}
	out := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if out == "" {
		return "Transcript"
	}
	return out
}
