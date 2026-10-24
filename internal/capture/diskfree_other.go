//go:build !windows

package capture

func diskFreeBytesOS(string) (int64, bool) { return 0, false }
