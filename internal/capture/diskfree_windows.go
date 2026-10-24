//go:build windows

package capture

import "golang.org/x/sys/windows"

// diskFreeBytes returns the free space available to the user on dir's drive.
func diskFreeBytesOS(dir string) (int64, bool) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, false
	}
	return int64(avail), true
}
