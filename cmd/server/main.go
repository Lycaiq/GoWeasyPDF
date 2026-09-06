// Package main es el punto de entrada y el único lugar donde resolvemos
// las dependencias concretas. Aquí hacemos el wiring: conectamos la infra
// con los casos de uso y los casos de uso con los handlers.
// Nada de lógica de negocio vive aquí.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	deliveryHTTP "github.com/arminbash/goweasyprint/internal/delivery/http"
	"github.com/arminbash/goweasyprint/internal/infra/weasyprint"
	"github.com/arminbash/goweasyprint/internal/usecase"
)

func main() {
	// slog con formato JSON es lo estándar en contenedores: Fluentd, Datadog,
	// Cloud Logging y similares esperan líneas JSON, no texto plano.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	port := envString("PORT", "8080")
	maxConcurrency := envInt("MAX_CONCURRENCY", 6)
	timeoutSecs := envInt("TIMEOUT_SECONDS", 60)

	// El wiring de dependencias sigue la dirección de Clean Architecture:
	// infra → usecase → delivery. Ninguna capa conoce a la que está por encima.
	converter := weasyprint.New("weasyprint")
	uc := usecase.New(converter, maxConcurrency)
	router := deliveryHTTP.NewRouter(uc, time.Duration(timeoutSecs)*time.Second)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
		// Estos timeouts de red son independientes del timeout de la petición.
		// Protegen contra clientes lentos que mantienen conexiones abiertas
		// sin enviar datos, lo que en un contenedor con concurrencia limitada
		// puede agotar los file descriptors disponibles.
		ReadTimeout:  70 * time.Second,
		WriteTimeout: 70 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown: esperamos que las peticiones en curso terminen
	// antes de apagar el servidor. Damos 30 segundos de margen; si alguna
	// petición todavía está en conversión, que termine antes de matar el proceso.
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit

		slog.Info("señal de apagado recibida, cerrando servidor")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			slog.Error("error durante el shutdown", "error", err)
		}
	}()

	slog.Info("servidor iniciado",
		"port", port,
		"max_concurrency", maxConcurrency,
		"timeout_seconds", timeoutSecs,
	)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("el servidor falló", "error", err)
		os.Exit(1)
	}
}

// envString lee una variable de entorno o devuelve el valor por defecto.
// Preferimos esto a una librería de configuración para mantener las dependencias
// externas al mínimo; el proyecto no necesita más que esto.
func envString(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// envInt lee una variable de entorno como entero o devuelve el valor por defecto.
// Si el valor no es parseable como int, ignoramos el error y usamos el default;
// un error silencioso aquí es mejor que un panic en el arranque por una typo en el env.
func envInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}
