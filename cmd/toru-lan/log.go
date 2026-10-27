package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// rotLog is the service log: one file, moved to <name>.1 when it reaches max
// bytes, so the log never holds more than about 2×max on disk.
type rotLog struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

const logMax = 1 << 20 // 1 MB

func openLog(path string, max int64) (*rotLog, error) {
	l := &rotLog{path: path, max: max}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *rotLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *rotLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		_ = l.f.Close()
		_ = os.Remove(l.path + ".1")
		_ = os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			l.f = nil
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// Logf writes one timestamped line.
func (l *rotLog) Logf(format string, args ...any) {
	_, _ = fmt.Fprintf(l, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

func (l *rotLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
