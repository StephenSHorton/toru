package agentctl

import (
	"os"
	"strings"
)

// ProfileSuffix is "-<name>" when TORU_PROFILE is set, else "". It is a
// developer hook for running a second Toru beside the installed one on the
// same PC (two people in one call, for testing): with it, the second copy
// takes its own single-instance lock and agent pipe, and `toru.exe ctl` /
// `toru.exe mcp` started with the same TORU_PROFILE talk to that copy. Point
// APPDATA and LOCALAPPDATA at other folders too, so its settings, room and
// WebView2 data are its own. Only letters, digits, '-' and '_' are kept.
func ProfileSuffix() string {
	var b strings.Builder
	for _, r := range os.Getenv("TORU_PROFILE") {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
		if b.Len() >= 32 {
			break
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "-" + b.String()
}

// ProfileBrowserArgs is TORU_BROWSER_ARGS split on spaces, but only for a
// TORU_PROFILE copy: extra WebView2 flags for a test copy, such as a fake
// microphone that plays a WAV file
// (--use-fake-device-for-media-stream --use-file-for-fake-audio-capture=x.wav).
// The installed Toru never reads it.
func ProfileBrowserArgs() []string {
	if ProfileSuffix() == "" {
		return nil
	}
	return strings.Fields(os.Getenv("TORU_BROWSER_ARGS"))
}
