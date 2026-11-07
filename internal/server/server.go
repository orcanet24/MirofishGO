// Package server — arranque compartido de los binarios MiroFish.
//
// mirofish.exe          → server.Run(false): multi-usuario (cuentas, planes,
//                         panel admin, aislamiento, LLM por usuario).
// mirofish-personal.exe → server.Run(true): binario personal como el original;
//                         acceso por ACCESS_KEY, sin cuentas, LLM global.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

const line50 = "=================================================="

// Run ejecuta el servidor hasta recibir SIGINT/SIGTERM. personal=true fuerza
// el modo personal aunque el .env no lo indique.
func Run(personal bool) {
	cfg := config.Load()
	if personal {
		cfg.PersonalMode = true
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "configuración errónea:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		fmt.Fprintln(os.Stderr, "\nrevisa el .env")
		os.Exit(1)
	}

	logx.SetBaseDir(cfg.BaseDir)
	logger := logx.Get("mirofish")

	logger.Info("=" + line50 + "=")
	if cfg.PersonalMode {
		logger.Info("MiroFish Personal (binario Go) arrancando...")
		logger.Info("modo personal: acceso por ACCESS_KEY, sin cuentas de usuario")
	} else {
		logger.Info("Mirofish Backend (binario Go) arrancando...")
	}
	logger.Info("=" + line50 + "=")
	logger.Info("clave de acceso: " + accessState(cfg))

	var handler http.Handler
	if cfg.PersonalMode {
		handler = httpx.PersonalRouter(cfg, logger)
	} else {
		handler = httpx.Router(cfg, logger)
	}

	server := &http.Server{
		Handler:           handler,
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

func accessState(cfg *config.Config) string {
	if cfg.AccessKey == "" {
		return "desactivada (ACCESS_KEY vacía) — solo desarrollo"
	}
	return "activa (ACCESS_KEY configurada)"
}

var _ *slog.Logger // trazas futuras
