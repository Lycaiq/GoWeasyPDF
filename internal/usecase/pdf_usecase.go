// Package usecase contiene la lógica de aplicación: qué hacemos con una petición
// antes de tocar la infraestructura. El caso de uso no sabe nada de HTTP ni de
// weasyprint; solo sabe que existe algo que convierte y un límite de concurrencia.
package usecase

import (
	"context"
	"fmt"

	"github.com/arminbash/goweasyprint/internal/domain"
)

// PDFGenerationUseCase orquesta una conversión controlando la presión sobre
// el sistema mediante un semáforo. Es el guardián entre el tráfico HTTP
// y los subprocesos del SO.
type PDFGenerationUseCase struct {
	converter  domain.PDFConverter
	semaphore  chan struct{}
}

// New construye el caso de uso con sus dependencias ya resueltas.
// maxConcurrency determina cuántas conversiones simultáneas permitimos.
// Un valor de 6 es conservador para ~1 GB de RAM; documentos pesados
// pueden requerir bajarlo, documentos ligeros podrían soportar más.
func New(converter domain.PDFConverter, maxConcurrency int) *PDFGenerationUseCase {
	return &PDFGenerationUseCase{
		converter: converter,
		// Un canal con buffer es la forma idiomática de Go para implementar
		// un semáforo de conteo. struct{} tiene tamaño cero; el canal solo
		// cuesta su propio overhead de sincronización.
		semaphore: make(chan struct{}, maxConcurrency),
	}
}

// Execute intenta adquirir un slot del semáforo y, si lo consigue, delega
// la conversión al PDFConverter. Si el semáforo está lleno, la goroutine
// espera hasta que se libere un slot o el contexto expire, lo que ocurra primero.
func (uc *PDFGenerationUseCase) Execute(ctx context.Context, req domain.ConversionRequest) (*domain.ConversionResult, error) {
	// El select con ctx.Done() es lo que hace que el semáforo sea "justo"
	// bajo presión. Sin esto, las peticiones esperarían en la cola de canal
	// indefinidamente aunque el cliente ya se haya ido o el timeout haya vencido.
	// Con esto, una petición que lleva 59 segundos esperando slot simplemente
	// falla limpiamente en lugar de bloquear una goroutine para siempre.
	select {
	case uc.semaphore <- struct{}{}:
		// Adquirimos el slot; lo liberamos cuando la función retorne,
		// sin importar si fue por éxito, error o pánico.
		defer func() { <-uc.semaphore }()
	case <-ctx.Done():
		// Timeout o cancelación mientras esperábamos en la cola del semáforo.
		// El mensaje diferencia entre "el sistema está saturado" y "el contexto
		// fue cancelado por otra razón", aunque en la práctica casi siempre
		// será lo primero.
		return nil, fmt.Errorf("no se pudo adquirir slot de conversión: %w", ctx.Err())
	}

	return uc.converter.Convert(ctx, req)
}
