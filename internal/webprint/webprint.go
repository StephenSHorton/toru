// Package webprint opens an HTML file in a WebView2 of its own, out of
// sight, so Go can print it to PDF or have the page render something and
// send it back (docs/whiteboard.md section 3: the board's PDF and PNG
// exports). It needs no Toru window: agents export boards with the board
// window closed.
//
// Each Open starts one WebView2 on a thread of its own, in a window placed
// off every screen, with its own user data folder (a WebView2 environment
// can't share Toru's folder with different options). PDF goes through
// ICoreWebView2_7.PrintToPdf, which Wails alpha.98 and its go-webview2 don't
// expose: the COM call is made here.
//
// The page talks to Go with window.chrome.webview.postMessage(string); Next
// waits for the next message with a given prefix.
package webprint

import (
	"errors"
	"time"
)

// Options for a page.
type Options struct {
	// DataDir is the WebView2 user data folder for these pages. Required.
	DataDir string
	// Width and Height are the window's size in pixels (1280×800 if 0).
	Width, Height int
	// Fragment is put after the file's address, after "#".
	Fragment string
	// URL, when set, is loaded instead of the file (path is then ignored):
	// a check's page on its own local server.
	URL string
	// Timeout bounds starting WebView2 and loading the page (30 s if 0).
	Timeout time.Duration

	// browserPath is a WebView2 runtime folder; tests give one that isn't
	// there, to see a failing WebView2 fail the page and not the process.
	browserPath string
}

// PDFOptions sets the printed page. Sizes are inches.
type PDFOptions struct {
	PageWidth, PageHeight float64
	// Scale is the print scale, 0.1 to 2 (1 if 0).
	Scale float64
	// Margins, in inches, on every side.
	Margin float64
	// Backgrounds prints background colours and pictures.
	Backgrounds bool
}

// ErrUnsupported is returned where there is no WebView2 (not Windows).
var ErrUnsupported = errors.New("webprint needs WebView2 (Windows)")
