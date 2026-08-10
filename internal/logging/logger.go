package logging

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelInfo:    "INFO",
	LevelSuccess: "OK",
	LevelWarn:    "WARN",
	LevelError:   "ERROR",
}

var levelColors = map[Level]string{
	LevelInfo:    "\033[0;34m", // Blue
	LevelSuccess: "\033[0;32m", // Green
	LevelWarn:    "\033[1;33m", // Yellow
	LevelError:   "\033[0;31m", // Red
}

const colorReset = "\033[0m"

// Logger writes to both terminal and a log file.
type Logger struct {
	stdout io.Writer
	stderr io.Writer
	file   *os.File
	quiet  bool
	module string
}

// New creates a logger that writes to stdout/stderr and a log file.
func New(quiet bool) (*Logger, error) {
	l := &Logger{
		stdout: os.Stdout,
		stderr: os.Stderr,
		quiet:  quiet,
	}

	// Open log file in user state directory
	stateDir, err := stateHomeDir()
	if err == nil {
		localStateDir := filepath.Join(stateDir, ".local")
		stateDataDir := filepath.Join(localStateDir, "state")
		appStateDir := filepath.Join(stateDataDir, "sys-bootstrap")
		logDir := filepath.Join(appStateDir, "logs")
		if err := os.MkdirAll(logDir, 0o755); err == nil {
			// MkdirAll may have created one or more parent directories as root.
			// Hand back the complete user-state path, not just the leaf, so the
			// invoking user can traverse it on later non-sudo runs.
			if err := system.ChownToInvokingUser(localStateDir, stateDataDir, appStateDir, logDir); err == nil {
				logFile := filepath.Join(logDir, fmt.Sprintf("sys-bootstrap-%s.log", time.Now().Format("20060102-150405")))
				f, openErr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if openErr == nil {
					if system.ChownToInvokingUser(logFile) == nil {
						l.file = f
					} else {
						_ = f.Close()
					}
				}
			}
		}
	}

	return l, nil
}

func stateHomeDir() (string, error) {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" && sudoUser != "root" {
		u, err := user.Lookup(sudoUser)
		if err == nil && u.HomeDir != "" {
			return u.HomeDir, nil
		}
	}
	return os.UserHomeDir()
}

// SetModule sets the current module name for log messages.
func (l *Logger) SetModule(name string) {
	l.module = name
}

func (l *Logger) write(level Level, msg string) {
	ts := time.Now().Format("15:04:05")
	prefix := ""
	if l.module != "" {
		prefix = "[" + l.module + "] "
	}

	// Colorized terminal output
	if !l.quiet {
		color := levelColors[level]
		terminal := l.stdout
		if level == LevelError {
			terminal = l.stderr
		}
		fmt.Fprintf(terminal, "%s[%s]%s %s%s\n", color, levelNames[level], colorReset, prefix, msg)
	}

	// Plain text to log file
	if l.file != nil {
		fmt.Fprintf(l.file, "%s [%s] %s%s\n", ts, levelNames[level], prefix, msg)
	}
}

func (l *Logger) Info(msg string)    { l.write(LevelInfo, msg) }
func (l *Logger) Success(msg string) { l.write(LevelSuccess, msg) }
func (l *Logger) Warn(msg string)    { l.write(LevelWarn, msg) }

func (l *Logger) Error(msg string) {
	l.write(LevelError, msg)
}

func (l *Logger) Infof(format string, args ...interface{}) {
	l.write(LevelInfo, fmt.Sprintf(format, args...))
}
func (l *Logger) Successf(format string, args ...interface{}) {
	l.write(LevelSuccess, fmt.Sprintf(format, args...))
}
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.write(LevelWarn, fmt.Sprintf(format, args...))
}
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.write(LevelError, fmt.Sprintf(format, args...))
}

// ErrorWithOutput logs an error with command output details.
func (l *Logger) ErrorWithOutput(msg string, exitCode int, stdout, stderr string) {
	l.Errorf("%s (exit code: %d)", msg, exitCode)
	if stdout != "" {
		l.Infof("stdout: %s", truncate(stdout, 500))
	}
	if stderr != "" {
		l.Errorf("stderr: %s", truncate(stderr, 500))
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// Close closes the log file.
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}
