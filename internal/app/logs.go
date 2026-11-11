package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Log rotation defaults for <state>/logs/daemon.log: 5 files x 5 MB.
const (
	LogMaxBytes = 5 << 20
	LogFiles    = 5
)

// LogDir is where daemon logs live.
func LogDir(stateDir string) string { return filepath.Join(stateDir, "logs") }

// RotatingLog is an io.WriteCloser appending to <dir>/<name> and rotating by
// size: name -> name.1 -> ... -> name.<files-1>; the oldest is dropped. It is
// safe for concurrent use inside one process.
type RotatingLog struct {
	mu    sync.Mutex
	path  string
	max   int64
	files int
	f     *os.File
	size  int64
}

// OpenLog opens (creating dir and file as needed) a rotating log.
func OpenLog(dir, name string, maxBytes int64, files int) (*RotatingLog, error) {
	if files < 1 {
		files = 1
	}
	if maxBytes < 1 {
		maxBytes = LogMaxBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l := &RotatingLog{path: filepath.Join(dir, name), max: maxBytes, files: files}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *RotatingLog) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

func (l *RotatingLog) rotate() error {
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
	for i := l.files - 1; i >= 1; i-- {
		from := l.path
		if i > 1 {
			from = fmt.Sprintf("%s.%d", l.path, i-1)
		}
		to := fmt.Sprintf("%s.%d", l.path, i)
		if _, err := os.Stat(from); err == nil {
			_ = os.Remove(to)
			_ = os.Rename(from, to)
		}
	}
	if l.files == 1 {
		_ = os.Remove(l.path)
	}
	return l.open()
}

func (l *RotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *RotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
