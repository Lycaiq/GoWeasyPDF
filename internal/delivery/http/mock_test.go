package http_test

import (
	"context"
	"time"

	"github.com/arminbash/goweasyprint/internal/domain"
)

// fakeConverter simula el comportamiento del conversor sin llamar a weasyprint.
// Tener el fake aquí, en el paquete de tests, garantiza que no se cuele
// en el binario de producción por accidente.
type fakeConverter struct {
	// delay simula el tiempo que tarda una conversión real.
	// Útil para forzar timeouts o saturar el semáforo en tests de estrés.
	delay time.Duration
	// err permite inyectar un error controlado para probar la ruta de error del handler.
	err error
	// pdfBytes es el contenido que devuelve como "PDF". En tests no nos importa
	// que sea un PDF válido, solo que el flujo lo trate como bytes opacos.
	pdfBytes []byte
}

func (f *fakeConverter) Convert(ctx context.Context, _ domain.ConversionRequest) (*domain.ConversionResult, error) {
	// Respetamos la cancelación del context incluso en el fake.
	// Sin esto, los tests de timeout esperarían siempre el delay completo,
	// haciendo la suite de tests innecesariamente lenta.
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if f.err != nil {
		return nil, f.err
	}

	pdf := f.pdfBytes
	if pdf == nil {
		pdf = []byte("%PDF-1.4 fake pdf content for testing")
	}

	return &domain.ConversionResult{PDF: pdf}, nil
}
