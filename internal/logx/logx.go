// Package logx — port of backend/app/utils/logger.py.
//
// Escritura doble: archivo detallado (DEBUG+, con nombre del logger y rotación
// 10MB / 5 backups) y consola simple (INFO+), igual que en Python.
package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RotatingWriter escribe a un archivo rotando a maxSize y conservando backups.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

func NewRotating(path string, maxBytes int64, keep int) *RotatingWriter {
	return &RotatingWriter{path: path, maxBytes: maxBytes, keep: keep}
}

func (w *RotatingWriter) rotateLocked() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	_ = os.Rename(w.path, w.path+".1")
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		_ = os.MkdirAll(filepath.Dir(w.path), 0o755)
		f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		st, _ := f.Stat()
		w.f, w.size = f, st.Size()
	}
	if w.size+int64(len(p)) > w.maxBytes {
		w.rotateLocked()
		f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		w.f, w.size = f, 0
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close closes the underlying file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

var (
	baseDirOnce sync.Once
	baseDir     string
	loggers     sync.Map // name -> *slog.Logger
	consoleMu   sync.Mutex

	consoleLevelOnce sync.Once
	consoleLevel     slog.Level
)

// SetBaseDir define el directorio donde se crea logs/. Llamar una vez al arranque.
func SetBaseDir(dir string) {
	baseDirOnce.Do(func() { baseDir = dir })
}

// consoleLevelFromEnv lee LOG_LEVEL del entorno/.env (debug|info|warn|error).
// Por defecto info: la consola muestra operaciones visibles y la consola solo
// se llena de detalle interno si el usuario lo pide.
func consoleLevelFromEnv() slog.Level {
	consoleLevelOnce.Do(func() {
		consoleLevel = slog.LevelInfo
		switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
		case "debug":
			consoleLevel = slog.LevelDebug
		case "warn", "warning":
			consoleLevel = slog.LevelWarn
		case "error":
			consoleLevel = slog.LevelError
		case "info", "":
			consoleLevel = slog.LevelInfo
		default:
			consoleLevel = slog.LevelInfo
			fmt.Println("warning: LOG_LEVEL desconocido; usando info")
		}
	})
	return consoleLevel
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

func formatLine(name, msg string, l slog.Level, t time.Time, simpleFmt bool) string {
	lvl := levelName(l)
	if simpleFmt {
		// Con el nombre del componente: así cada línea de consola dice de
		// qué parte del backend salió (mirofish, mirofish.simx, ...).
		return fmt.Sprintf("[%s] %s [%s]: %s\n", t.Format("15:04:05"), lvl, name, msg)
	}
	return fmt.Sprintf("[%s] %s [%s] %s\n", t.Format("2006-01-02 15:04:05"), lvl, name, msg)
}

// Handler enruta cada registro al archivo detallado y, si aplica, a consola.
type Handler struct {
	name    string
	file    *RotatingWriter
	console io // nil => sin salida por consola (hilos de fondo)
}

type io = *os.File

var _ slog.Handler = (*Handler)(nil)

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelDebug }

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	msg := b.String()
	if h.console != nil && r.Level >= consoleLevelFromEnv() {
		consoleMu.Lock()
		fmt.Fprint(h.console, formatLine(h.name, msg, r.Level, r.Time, true))
		consoleMu.Unlock()
	}
	fmt.Fprint(h.file, formatLine(h.name, msg, r.Level, r.Time, false))
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *Handler) WithGroup(name string) slog.Handler       { return h }

var _ io = os.Stdout

// Get devuelve (y crea si es necesario) el logger con el nombre dado.
func Get(name string) *slog.Logger {
	if v, ok := loggers.Load(name); ok {
		return v.(*slog.Logger)
	}
	dir := baseDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	file := NewRotating(filepath.Join(dir, "logs", time.Now().Format("2006-01-02")+".log"), 10*1024*1024, 5)
	logger := slog.New(&Handler{name: name, file: file, console: os.Stdout})
	loggers.Store(name, logger)
	return logger
}

// Background devuelve un logger igual pero sin salida por consola, para
// pipelines de fondo (equivalente a los handlers extra que Python crea por
// componente en concurrencia).
func Background(name string) *slog.Logger {
	if v, ok := loggers.Load(name + "::bg"); ok {
		return v.(*slog.Logger)
	}
	dir := baseDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	file := NewRotating(filepath.Join(dir, "logs", time.Now().Format("2006-01-02")+".log"), 10*1024*1024, 5)
	logger := slog.New(&Handler{name: name, file: file, console: nil})
	loggers.Store(name+"::bg", logger)
	return logger
}

// Helpers a nivel de paquete, espejo de logger.py.
func Debug(name, msg string) { Get(name).Debug(msg) }
func Info(name, msg string)  { Get(name).Info(msg) }
func Warn(name, msg string)  { Get(name).Warn(msg) }
func Error(name, msg string) { Get(name).Error(msg) }
