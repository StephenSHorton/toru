package vid

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// fontsKey lists installed fonts: in HKLM for everyone (values are file
// names under the Windows Fonts folder) and in HKCU for fonts installed for
// one user (values are full paths, usually under
// %LOCALAPPDATA%\Microsoft\Windows\Fonts).
const fontsKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`

// fontFiles is every installed font file drawtext can open.
func fontFiles() []string {
	winFonts := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	if os.Getenv("WINDIR") == "" {
		winFonts = `C:\Windows\Fonts`
	}
	var out []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, fontsKey, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, n := range names {
			v, _, err := k.GetStringValue(n)
			if err != nil || v == "" {
				continue
			}
			if !filepath.IsAbs(v) {
				v = filepath.Join(winFonts, v)
			}
			if isFontFile(v) {
				out = append(out, v)
			}
		}
		_ = k.Close()
	}
	return out
}
