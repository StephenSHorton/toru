//go:build !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// dropTopmostBehindForeground clears AlwaysOnTop; off Windows there's no
// z-order quirk to work around.
func dropTopmostBehindForeground(win *application.WebviewWindow) {
	if win != nil {
		win.SetAlwaysOnTop(false)
	}
}
