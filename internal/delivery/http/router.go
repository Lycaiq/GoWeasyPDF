package http

import (
	"net/http"
	"time"

	"github.com/arminbash/goweasyprint/internal/domain"
)

// NewRouter arma el mux con la cadena de middlewares y registra las rutas.
// Lo mantenemos separado del main para poder instanciarlo en tests sin
// arrancar un servidor real.
func NewRouter(uc domain.PDFUseCase, timeoutDuration time.Duration) http.Handler {
	mux := http.NewServeMux()

	handler := NewGeneratorHandler(uc)
	mux.Handle("POST /generator", handler)

	// La cadena de middlewares se aplica de afuera hacia adentro:
	// Logging ve la petición primero y el resultado último,
	// RequestID entra antes que el handler para que el ID esté en context,
	// Timeout crea el deadline que heredan todas las goroutines hijas.
	return Logging(RequestID(Timeout(timeoutDuration)(mux)))
}
