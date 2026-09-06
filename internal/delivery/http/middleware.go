package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// requestIDKey es un tipo privado para la clave del context.
// Usar un tipo propio en lugar de un string literal previene colisiones
// con otras librerías que también guarden cosas en el context con claves string.
type requestIDKey struct{}

// RequestIDFromContext es la función pública para que cualquier capa
// pueda extraer el request ID sin conocer el tipo de la clave.
// Devolver string vacío en lugar de un segundo valor de error es deliberado:
// en los logs preferimos un campo vacío a un panic por type assertion.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// RequestID lee o genera el header x-request-id y lo inyecta en el context.
// Propagarlo en el context (no solo en el header) permite que las capas internas
// lo adjunten a sus logs sin que el handler tenga que pasarlo manualmente
// como argumento por toda la cadena de llamadas.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-request-id")
		if id == "" {
			// Si el cliente no envía ID generamos uno aquí. Esto es útil para
			// correlacionar logs de una petición que llega sin trazabilidad.
			id = uuid.NewString()
		}

		// Reenviamos el ID en la respuesta para que el cliente pueda usarlo
		// al reportar un bug o buscar en los logs.
		w.Header().Set("x-request-id", id)

		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Timeout envuelve cada petición en un context con deadline de 60 segundos.
// Cuando el deadline vence, el context se cancela y exec.CommandContext mata
// el subproceso de weasyprint automáticamente.
//
// Usamos http.TimeoutHandler de la stdlib en lugar de implementar el deadline
// a mano porque ya maneja el caso edge de que la respuesta empiece a escribirse
// antes de que expire el timeout.
func Timeout(duration time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, duration, `{"error":"tiempo de espera agotado"}`)
	}
}

// Logging registra el resultado de cada petición con sus metadatos esenciales.
// Va al final de la cadena de middlewares para capturar el status real que
// devolvió el handler, no el que entró.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Necesitamos interceptar el status code que escribe el handler;
		// el ResponseWriter original no lo expone después de escribirlo.
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		slog.InfoContext(r.Context(), "peticion completada",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", RequestIDFromContext(r.Context()),
		)
	})
}

// responseWriter es un wrapper mínimo sobre http.ResponseWriter que captura
// el status code. Solo implementamos lo estrictamente necesario; no hay que
// reimplementar toda la interfaz si no la vamos a usar.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}
