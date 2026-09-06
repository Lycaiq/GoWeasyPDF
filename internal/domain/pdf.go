// Package domain contiene las abstracciones centrales del sistema.
// Nada de este paquete debe importar capas externas; la dependencia fluye hacia aquí,
// no desde aquí hacia afuera.
package domain

import "context"

// ConversionRequest encapsula lo que el cliente quiere convertir.
// Usamos bytes crudos en lugar de string para no cargar con la codificación
// en capa de dominio; quien llama ya hizo el decode de Base64.
type ConversionRequest struct {
	HTML []byte
}

// ConversionResult es lo que el sistema promete devolver al cliente.
// El contrato es sencillo: si no hay error, PDF tiene contenido.
type ConversionResult struct {
	PDF []byte
}

// PDFConverter define el contrato para cualquier cosa que sepa convertir HTML a PDF.
// Al ser una interfaz en dominio, podemos intercambiar weasyprint por puppeteer
// o cualquier otra bestia sin tocar el caso de uso ni el handler.
type PDFConverter interface {
	Convert(ctx context.Context, req ConversionRequest) (*ConversionResult, error)
}

// PDFUseCase define el contrato de la lógica de aplicación.
// La separación respecto a PDFConverter no es burocracia: el caso de uso
// es quien controla el semáforo y la política de reintentos; el converter
// solo sabe hablar con el binario externo.
type PDFUseCase interface {
	Execute(ctx context.Context, req ConversionRequest) (*ConversionResult, error)
}
