// MiroFish Go — binario único: API HTTP + clave única de acceso.
//
// Port de backend/run.py + app/__init__.py. El resto de componentes
// (OASIS, runner, frontend embebido) se monta por hitos; el grafo y el
// estado persistente viven en MySQL 8 / MariaDB.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mirofish/internal/config"
	"mirofish/internal/httpx"
	"mirofish/internal/logx"
)

func main() {
	cfg := config.Load()

	// Validación como run.py: aborta con lista de errores de configuración.
	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "configuración errónea:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		fmt.Fprintln(os.Stderr, "\nrevisa el .env")
		os.Exit(1)
	}

	// Logs: bajo BaseDir (como setup_logger lo hace en backend).
	logx.SetBaseDir(cfg.BaseDir)
	logger := logx.Get("mirofish")

	logger.Info("=" + line50 + "=")
	logger.Info("MiroFish Backend (binario Go) arrancando...")
	logger.Info("=" + line50 + "=")
	logger.Info("clave de acceso: " + accessState(cfg))

	// El frontend aún no se sirve desde el binario (hito 8: go:embed).
	server := &http.Server{
		Handler:           httpx.Router(cfg, logger),
		ReadHeaderTimeout: 15 * time.Second,
		// No hay cap de tiempo de respuesta: las simulaciones y streaming
		// pueden ser largos (igual que Flask threaded).
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("no se pudo escuchar en " + addr + ": " + err.Error())
		os.Exit(1)
	}
	logger.Info("escuchando en http://" + addr + " (health: /health, readiness: /ready)")

	// Graceful shutdown como SimulationRunner.register_cleanup lo hace en Python.
	done := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		logger.Info("señal de cierre recibida; apagando con gracia")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		close(done)
	}()

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("error del servidor: " + err.Error())
		os.Exit(1)
	}
	<-done
	logger.Info("servidor detenido")
}

const line50 = "=================================================="

func accessState(cfg *config.Config) string {
	if cfg.AccessKey == "" {
		return "desactivada (ACCESS_KEY vacía) — solo desarrollo"
	}
	return "activa (ACCESS_KEY configurada)"
}
