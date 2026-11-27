//go:build windows

package main

import (
	"os"
	"testing"
	"time"
)

// webViewDir is a folder for a WebView2's data that outlives the test's own
// temp folder. WebView2's browser processes can hold a file in it (such as
// BrowserMetrics) for a while after the page closes, more so on a busy
// machine, and t.TempDir fails the test when it cannot remove its folder.
// The cleanup here retries for a while, then leaves the folder behind with a
// log line instead of failing.
func webViewDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "toru-webview-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(15 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Logf("left %s behind: WebView2 still holds a file in it: %v", dir, err)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
	return dir
}
