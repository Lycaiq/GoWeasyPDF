package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/arminbash/goweasyprint/internal/domain"
)

// generatorRequest es lo que esperamos recibir del cliente.
// El HTML viene en Base64 para que el JSON sea siempre válido,
// independientemente de la codificación o del contenido del HTML.
type generatorRequest struct {
	HTMLB64 string `json:"html_base64"`
}

// generatorResponse es lo que le devolvemos al cliente.
type generatorResponse struct {
	PDFB64 string `json:"pdf_base64"`
}

// GeneratorHandler maneja el endpoint POST /generator.
// Recibe el caso de uso por inyección de dependencias; el handler no sabe
// si detrás hay weasyprint, puppeteer o un PDF pregenerado.
type GeneratorHandler struct {
	useCase domain.PDFUseCase
}

// NewGeneratorHandler construye el handler con sus dependencias.
func NewGeneratorHandler(uc domain.PDFUseCase) *GeneratorHandler {
	return &GeneratorHandler{useCase: uc}
}

// ServeHTTP implementa http.Handler para que podamos registrar el handler
// directamente en el router sin wrappear en un HandlerFunc.
func (h *GeneratorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Limitamos el tamaño del body para que alguien no nos mande un HTML de 500MB
	// y nos tumbe el servidor antes de llegar al semáforo. 50MB debería ser más
	// que suficiente para cualquier documento razonable.
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	// Leemos el body completo antes de parsear el JSON. Así el error de
	// MaxBytesReader se propaga limpiamente sin que json.Decoder lo envuelva
	// en su propia capa de error, lo que hace casi imposible hacer errors.As.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
				Error: "el body supera el límite de 50MB",
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "no se pudo leer el body de la petición",
		})
		return
	}

	if len(rawBody) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "el body está vacío",
		})
		return
	}

	var req generatorRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "JSON inválido: " + err.Error(),
		})
		return
	}

	if req.HTMLB64 == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "el campo html_base64 es obligatorio",
		})
		return
	}

	htmlBytes, err := base64.StdEncoding.DecodeString(req.HTMLB64)
	if err != nil {
		// Base64 inválido es un error del cliente, no del servidor.
		// Lo logueamos en debug porque en producción puede ser ruido.
		slog.DebugContext(ctx, "base64 inválido recibido",
			"request_id", RequestIDFromContext(ctx),
			"error", err,
		)
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: "html_base64 no es Base64 válido",
		})
		return
	}

	result, err := h.useCase.Execute(ctx, domain.ConversionRequest{HTML: htmlBytes})
	if err != nil {
		// No exponemos el error interno al cliente. El log ya tiene el detalle.
		// ctx.Err() nos dice si fue timeout o cancelación; cualquier otra cosa
		// es un fallo de infraestructura que merece un 500.
		if ctx.Err() != nil {
			// El middleware de Timeout ya habrá escrito 503 si venció el deadline.
			// Esta rama cubre el caso en que el cliente cerró la conexión.
			return
		}
		slog.ErrorContext(ctx, "fallo en la conversión",
			"request_id", RequestIDFromContext(ctx),
			"error", err,
		)
		writeJSON(w, http.StatusInternalServerError, errorResponse{
			Error: "error interno al generar el PDF",
		})
		return
	}

	writeJSON(w, http.StatusOK, generatorResponse{
		PDFB64: base64.StdEncoding.EncodeToString(result.PDF),
	})
}
