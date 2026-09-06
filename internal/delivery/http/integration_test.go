// Package http_test contiene los tests de integración de la capa de entrega.
// Usamos el sufijo _test en el nombre del paquete para importar solo la API
// pública y asegurarnos de que probamos lo mismo que ve un consumidor externo.
package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deliveryHTTP "github.com/arminbash/goweasyprint/internal/delivery/http"
	"github.com/arminbash/goweasyprint/internal/domain"
	"github.com/arminbash/goweasyprint/internal/usecase"
)

// newTestServer arma un servidor de prueba completo: converter fake → usecase → router.
// Así los tests pasan por el semáforo, los middlewares y el handler real,
// no por mocks parciales que ignoran la mayor parte del flujo.
func newTestServer(t *testing.T, converter *fakeConverter, maxConcurrency int, timeout time.Duration) *httptest.Server {
	t.Helper()
	uc := usecase.New(converter, maxConcurrency)
	router := deliveryHTTP.NewRouter(uc, timeout)
	return httptest.NewServer(router)
}

// buildBody construye el JSON de la petición codificando el HTML en Base64.
func buildBody(t *testing.T, html string) io.Reader {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(html))
	body, err := json.Marshal(map[string]string{"html_base64": encoded})
	if err != nil {
		t.Fatalf("no se pudo serializar el body de prueba: %v", err)
	}
	return bytes.NewReader(body)
}

// postGenerator es un helper que hace el POST /generator y devuelve la respuesta.
// Evita repetir la misma secuencia de http.NewRequest + client.Do en cada test.
func postGenerator(t *testing.T, srv *httptest.Server, body io.Reader) *http.Response {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+"/generator", "application/json", body)
	if err != nil {
		t.Fatalf("petición al servidor de prueba fallida: %v", err)
	}
	return resp
}

// --- Tests de flujo feliz ---

func TestGeneratorHandler_ConversionExitosa(t *testing.T) {
	pdfEsperado := []byte("%PDF-1.4 contenido de prueba")
	srv := newTestServer(t, &fakeConverter{pdfBytes: pdfEsperado}, 4, 5*time.Second)
	defer srv.Close()

	resp := postGenerator(t, srv, buildBody(t, "<html><body>Hola</body></html>"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("esperaba 200, recibí %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("no se pudo decodificar la respuesta: %v", err)
	}

	pdfB64, ok := result["pdf_base64"]
	if !ok || pdfB64 == "" {
		t.Fatal("la respuesta no contiene pdf_base64")
	}

	decoded, err := base64.StdEncoding.DecodeString(pdfB64)
	if err != nil {
		t.Fatalf("pdf_base64 no es Base64 válido: %v", err)
	}

	if !bytes.Equal(decoded, pdfEsperado) {
		t.Errorf("PDF recibido no coincide con el esperado")
	}
}

func TestGeneratorHandler_PropagaRequestID(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/generator", buildBody(t, "<html></html>"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", "test-id-12345")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("petición fallida: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("x-request-id"); got != "test-id-12345" {
		t.Errorf("esperaba x-request-id=test-id-12345, recibí %q", got)
	}
}

func TestGeneratorHandler_GeneraRequestIDSiNoViene(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	// No enviamos x-request-id; el middleware debe generarlo.
	resp := postGenerator(t, srv, buildBody(t, "<html></html>"))
	defer resp.Body.Close()

	if rid := resp.Header.Get("x-request-id"); rid == "" {
		t.Error("el servidor debería generar un x-request-id cuando el cliente no lo envía")
	}
}

// --- Tests de peticiones malformadas ---

func TestGeneratorHandler_BodyVacio(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	resp := postGenerator(t, srv, strings.NewReader(""))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("esperaba 400, recibí %d", resp.StatusCode)
	}
}

func TestGeneratorHandler_JSONMalformado(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	resp := postGenerator(t, srv, strings.NewReader("{esto no es json"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("esperaba 400, recibí %d", resp.StatusCode)
	}
}

func TestGeneratorHandler_CampoFaltante(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	// JSON válido pero sin el campo html_base64.
	resp := postGenerator(t, srv, strings.NewReader(`{"otro_campo": "valor"}`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("esperaba 400, recibí %d", resp.StatusCode)
	}
}

func TestGeneratorHandler_Base64Invalido(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	resp := postGenerator(t, srv, strings.NewReader(`{"html_base64": "esto!no@es#base64$valido"}`))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("esperaba 400, recibí %d", resp.StatusCode)
	}
}

func TestGeneratorHandler_BodyDemasiadoGrande(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	// 51 MB de datos para superar el límite de 50 MB del handler.
	cuerpoGrande := strings.NewReader(strings.Repeat("x", 51<<20))
	resp := postGenerator(t, srv, cuerpoGrande)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("esperaba 413, recibí %d", resp.StatusCode)
	}
}

func TestGeneratorHandler_MetodoNoPermitido(t *testing.T) {
	srv := newTestServer(t, &fakeConverter{}, 4, 5*time.Second)
	defer srv.Close()

	// El mux de la stdlib 1.22 devuelve 405 para métodos no registrados.
	resp, err := srv.Client().Get(srv.URL + "/generator")
	if err != nil {
		t.Fatalf("petición GET fallida: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("esperaba 405, recibí %d", resp.StatusCode)
	}
}

// --- Tests de error de infraestructura ---

func TestGeneratorHandler_ErrorDeConversion(t *testing.T) {
	converter := &fakeConverter{err: errors.New("weasyprint: error simulado")}
	srv := newTestServer(t, converter, 4, 5*time.Second)
	defer srv.Close()

	resp := postGenerator(t, srv, buildBody(t, "<html></html>"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("esperaba 500, recibí %d", resp.StatusCode)
	}

	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	// El mensaje de error no debe filtrar detalles internos al cliente.
	if strings.Contains(body["error"], "weasyprint") {
		t.Error("la respuesta de error no debería exponer detalles internos del sistema")
	}
}

// --- Tests de timeout ---

func TestGeneratorHandler_TimeoutExpira(t *testing.T) {
	// El converter tarda 500ms pero el servidor solo espera 100ms.
	converter := &fakeConverter{delay: 500 * time.Millisecond}
	srv := newTestServer(t, converter, 4, 100*time.Millisecond)
	defer srv.Close()

	resp := postGenerator(t, srv, buildBody(t, "<html></html>"))
	defer resp.Body.Close()

	// http.TimeoutHandler devuelve 503 cuando el deadline vence.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("esperaba 503 por timeout, recibí %d", resp.StatusCode)
	}

	rawBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(rawBody), "tiempo de espera agotado") {
		t.Errorf("el cuerpo del timeout debería indicar el motivo, recibí: %s", rawBody)
	}
}

// --- Tests de concurrencia y semáforo ---

func TestGeneratorHandler_ConcurrenciaControlada(t *testing.T) {
	// Esta es la prueba de estrés principal: disparamos más peticiones simultáneas
	// que el límite del semáforo y verificamos que todas terminan correctamente,
	// que ninguna goroutine se queda colgada, y que el contador de en-vuelo
	// nunca supera maxConcurrency.
	const (
		maxConcurrency = 3
		totalRequests  = 12
		convDelay      = 50 * time.Millisecond
	)

	var (
		// inFlight mide cuántas conversiones corren al mismo tiempo en el fake.
		inFlight    atomic.Int32
		peakInFlight atomic.Int32
	)

	converter := &fakeConverter{
		delay: convDelay,
	}

	// Wrapeamos el fake para medir in-flight sin modificar su lógica.
	// Un closure sobre el fakeConverter es más limpio que añadir instrumentación
	// directamente en la struct que ya tiene su propio propósito.
	instrumentedConverter := &instrumentedFakeConverter{
		inner:        converter,
		inFlight:     &inFlight,
		peakInFlight: &peakInFlight,
	}

	srv := newTestServer(t, nil, maxConcurrency, 5*time.Second)
	defer srv.Close()

	// Reconstruimos el servidor con el converter instrumentado.
	// newTestServer no acepta un converter genérico; creamos uno ad-hoc.
	uc := usecase.New(instrumentedConverter, maxConcurrency)
	router := deliveryHTTP.NewRouter(uc, 5*time.Second)
	srvInstrumented := httptest.NewServer(router)
	defer srvInstrumented.Close()

	var wg sync.WaitGroup
	successCount := atomic.Int32{}

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			html := fmt.Sprintf("<html><body>petición %d</body></html>", n)
			resp, err := srvInstrumented.Client().Post(
				srvInstrumented.URL+"/generator",
				"application/json",
				buildBody(t, html),
			)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Todas las peticiones deberían tener éxito: el semáforo las serializa,
	// no las descarta, siempre que el timeout sea generoso.
	if got := successCount.Load(); got != totalRequests {
		t.Errorf("esperaba %d respuestas exitosas, obtuve %d", totalRequests, got)
	}

	// El pico de conversiones simultáneas nunca debe superar el semáforo.
	if peak := peakInFlight.Load(); peak > maxConcurrency {
		t.Errorf("el semáforo falló: se llegaron a ejecutar %d conversiones simultáneas (límite: %d)", peak, maxConcurrency)
	}
}

func TestGeneratorHandler_SemaforoRechazaBajoTimeout(t *testing.T) {
	// Con un timeout muy corto y conversiones lentas, las peticiones que no
	// consigan slot del semáforo a tiempo deben fallar con 503, no bloquearse.
	const (
		maxConcurrency = 2
		totalRequests  = 6
	)

	// El converter tarda 300ms, el timeout es 100ms.
	// Las dos primeras peticiones adquieren el semáforo; las demás agotan
	// el timeout esperando slot.
	converter := &fakeConverter{delay: 300 * time.Millisecond}
	srv := newTestServer(t, converter, maxConcurrency, 100*time.Millisecond)
	defer srv.Close()

	var wg sync.WaitGroup
	var timeouts atomic.Int32

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := srv.Client().Post(
				srv.URL+"/generator",
				"application/json",
				buildBody(t, "<html></html>"),
			)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusServiceUnavailable {
				timeouts.Add(1)
			}
		}()
	}

	wg.Wait()

	// Esperamos que al menos algunas peticiones hayan agotado el timeout.
	// No fijamos un número exacto porque el scheduler del SO puede variar,
	// pero con 6 peticiones y solo 2 slots, alguna tiene que caer.
	if timeouts.Load() == 0 {
		t.Error("esperaba que al menos una petición agotara el timeout esperando el semáforo")
	}
}

// instrumentedFakeConverter implementa domain.PDFConverter y mide la concurrencia real.
// Lo necesitamos separado del fakeConverter porque queremos medir in-flight
// sin mezclar responsabilidades en el fake base.
type instrumentedFakeConverter struct {
	inner        *fakeConverter
	inFlight     *atomic.Int32
	peakInFlight *atomic.Int32
}

func (c *instrumentedFakeConverter) Convert(ctx context.Context, req domain.ConversionRequest) (*domain.ConversionResult, error) {
	current := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)

	// Actualizamos el pico con compare-and-swap para que sea thread-safe.
	for {
		peak := c.peakInFlight.Load()
		if current <= peak {
			break
		}
		if c.peakInFlight.CompareAndSwap(peak, current) {
			break
		}
	}

	return c.inner.Convert(ctx, req)
}
