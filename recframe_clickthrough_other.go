//go:build !windows

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// makeWindowClickThrough is a no-op off Windows. The recorded-region indicator —
// like the rest of the capture pipeline — is Windows-only; this stub just keeps
// the package compiling on other platforms (CI / go vet).
func makeWindowClickThrough(*application.WebviewWindow) {}
