// Package logging is the Go replacement for the TypeScript LoggerService,
// ConsoleLogger and InstanceLoggerService modules. It keeps the same breadcrumb
// semantics used by the legacy log lines so existing log scrapers keep working.
package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level is a log severity.
type Level int32

const (
	LevelTrace Level = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelOff
)

// ParseLevel maps a textual level to a Level.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "off", "none":
		return LevelOff
	default:
		return LevelInfo
	}
}

func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelOff:
		return "OFF"
	default:
		return "INFO"
	}
}

// Logger is a leveled, breadcrumb-aware logger. The zero value is not usable;
// use New or one of the helpers.
type Logger struct {
	mu         sync.Mutex
	out        io.Writer
	level      atomic.Int32
	namespace  string
	breadcrumb []string
	json       bool
	now        func() time.Time
}

// Options configures a root logger.
type Options struct {
	Writer    io.Writer
	Level     Level
	Namespace string
	JSON      bool
	Now       func() time.Time
}

// New creates a root logger.
func New(opts Options) *Logger {
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	ns := opts.Namespace
	if ns == "" {
		ns = "pockethost"
	}
	l := &Logger{out: opts.Writer, namespace: ns, json: opts.JSON, now: opts.Now}
	l.level.Store(int32(opts.Level))
	return l
}

// Discard returns a logger that writes nowhere. Useful in tests.
func Discard() *Logger {
	return New(Options{Writer: io.Discard, Level: LevelOff})
}

// SetLevel changes the minimum level at runtime.
func (l *Logger) SetLevel(lv Level) { l.level.Store(int32(lv)) }

// Level returns the current minimum level.
func (l *Logger) Level() Level { return Level(l.level.Load()) }

// Create derives a child logger in the same style as `logger.create(name)`.
func (l *Logger) Create(name string) *Logger {
	if l == nil {
		return Discard()
	}
	c := &Logger{
		out:        l.out,
		namespace:  l.namespace + "/" + name,
		breadcrumb: append([]string(nil), l.breadcrumb...),
		json:       l.json,
		now:        l.now,
	}
	c.level.Store(l.level.Load())
	return c
}

// Breadcrumb returns a child logger with an extra breadcrumb segment, matching
// `logger.breadcrumb(x)`.
func (l *Logger) Breadcrumb(parts ...string) *Logger {
	if l == nil {
		return Discard()
	}
	c := &Logger{
		out:        l.out,
		namespace:  l.namespace,
		breadcrumb: append(append([]string(nil), l.breadcrumb...), parts...),
		json:       l.json,
		now:        l.now,
	}
	c.level.Store(l.level.Load())
	return c
}

// Enabled reports whether a level would be emitted.
func (l *Logger) Enabled(lv Level) bool {
	return l != nil && lv >= l.Level() && l.Level() != LevelOff
}

// Tracef, Debugf, Infof, Warnf and Errorf are the primary logging entrypoints.
func (l *Logger) Tracef(format string, args ...any) { l.logf(LevelTrace, format, args...) }
func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, format, args...) }

// Info is the variadic form used throughout the ported services.
func (l *Logger) Info(args ...any)  { l.log(LevelInfo, args...) }
func (l *Logger) Debug(args ...any) { l.log(LevelDebug, args...) }
func (l *Logger) Warn(args ...any)  { l.log(LevelWarn, args...) }
func (l *Logger) Error(args ...any) { l.log(LevelError, args...) }
func (l *Logger) Trace(args ...any) { l.log(LevelTrace, args...) }

// Dbg is the exact alias used by ported TypeScript code (`const { dbg } = logger`).
func (l *Logger) Dbg(args ...any) { l.log(LevelDebug, args...) }

// Raw writes an unformatted line regardless of level (used for child process
// stdout/stderr passthrough).
func (l *Logger) Raw(line string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.out, line)
}

func (l *Logger) logf(lv Level, format string, args ...any) {
	if !l.Enabled(lv) {
		return
	}
	l.emit(lv, fmt.Sprintf(format, args...))
}

func (l *Logger) log(lv Level, args ...any) {
	if !l.Enabled(lv) {
		return
	}
	l.emit(lv, strings.TrimSpace(fmt.Sprintln(args...)))
}

func (l *Logger) emit(lv Level, msg string) {
	ts := l.now().UTC().Format(time.RFC3339Nano)
	ns := l.namespace
	if len(l.breadcrumb) > 0 {
		ns += " " + strings.Join(l.breadcrumb, " ")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.json {
		fmt.Fprintf(l.out, `{"time":%q,"level":%q,"ns":%q,"msg":%q}`+"\n", ts, lv.String(), ns, msg)
		return
	}
	fmt.Fprintf(l.out, "%s %-5s [%s] %s\n", ts, lv.String(), ns, msg)
}

// Field is a structured key/value pair.
type Field struct {
	Key   string
	Value any
}

// F builds a Field.
func F(key string, value any) Field { return Field{Key: key, Value: value} }

// WithFields emits a single structured line. It is a drop-in replacement for the
// `dbg(msg, { e })` calls in the original services.
func (l *Logger) WithFields(lv Level, msg string, fields ...Field) {
	if !l.Enabled(lv) {
		return
	}
	if len(fields) == 0 {
		l.emit(lv, msg)
		return
	}
	var b strings.Builder
	b.WriteString(msg)
	for _, f := range fields {
		fmt.Fprintf(&b, " %s=%v", f.Key, f.Value)
	}
	l.emit(lv, b.String())
}
