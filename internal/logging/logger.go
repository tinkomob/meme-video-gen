package logging

import (
	"io"
	"log"
	"os"
	"sync"
)

type Logger struct {
	info  *log.Logger
	err   *log.Logger
	errMu sync.Mutex
	errW  io.WriteCloser
}

func New(errorsPath string) (*Logger, error) {
	// Clear the log file on startup
	if err := os.Truncate(errorsPath, 0); err != nil && !os.IsNotExist(err) {
		// If file doesn't exist, that's fine, we'll create it below
		if !os.IsNotExist(err) {
			return nil, err
		}
	}

	f, err := os.OpenFile(errorsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	// Write errors to both stdout and file
	errWriter := io.MultiWriter(os.Stdout, f)
	l := &Logger{
		info: log.New(os.Stdout, "INFO ", log.LstdFlags|log.Lmicroseconds),
		err:  log.New(errWriter, "ERROR ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile),
		errW: f,
	}
	return l, nil
}

func (l *Logger) Close() error {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	if l.errW != nil {
		return l.errW.Close()
	}
	return nil
}

func (l *Logger) Infof(format string, args ...any) {
	l.info.Printf(format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	l.err.Printf(format, args...)
}

func (l *Logger) Error(err error) {
	if err == nil {
		return
	}
	l.Errorf("%v", err)
}
