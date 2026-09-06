// Package weasyprint es el adaptador que sabe hablar con el binario externo.
// Todo lo feo de tratar con un proceso del SO vive aquí; el resto del sistema
// no necesita saber que existe exec.Command.
package weasyprint

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/arminbash/goweasyprint/internal/domain"
)

// Converter invoca el CLI de weasyprint en un subproceso efímero.
// El campo binPath existe para que en tests podamos apuntar a un fake
// sin tocar variables de entorno globales.
type Converter struct {
	binPath string
}

// New construye un Converter listo para usar.
// En producción binPath será simplemente "weasyprint" y el SO lo buscará en PATH.
// Si algún día necesitamos apuntar a una instalación específica dentro del contenedor,
// se pasa la ruta absoluta sin tocar nada más.
func New(binPath string) *Converter {
	return &Converter{binPath: binPath}
}

// Convert lanza weasyprint como subproceso, le pasa el HTML por STDIN y lee el PDF
// de STDOUT. Cero escrituras en disco: todo viaja por pipes del kernel.
//
// La firma "-" "-" le dice a weasyprint que lea de stdin y escriba en stdout,
// que es exactamente lo que necesitamos para no depender del filesystem del contenedor.
func (c *Converter) Convert(ctx context.Context, req domain.ConversionRequest) (*domain.ConversionResult, error) {
	// exec.CommandContext es importante aquí, no exec.Command.
	// Cuando el contexto expira (timeout de 60s o cliente desconectado),
	// Go manda SIGKILL al subproceso automáticamente. Sin esto, un weasyprint
	// colgado podría quedarse en memoria bloqueando el slot del semáforo para siempre.
	cmd := exec.CommandContext(ctx, c.binPath, "-", "-")

	cmd.Stdin = bytes.NewReader(req.HTML)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		// Logueamos stderr de weasyprint porque ahí viven los mensajes útiles
		// (CSS no soportado, fuentes faltantes, HTML malformado, etc.).
		// Sin esto, los errores de producción son imposibles de diagnosticar.
		slog.ErrorContext(ctx, "weasyprint falló",
			"error", err,
			"stderr", stderrBuf.String(),
		)

		// No exponemos stderr raw al cliente; puede tener rutas internas del contenedor.
		// El caller decide cómo traducir este error a HTTP.
		return nil, fmt.Errorf("el proceso de conversión terminó con error: %w", err)
	}

	// Si stdout quedó vacío y no hubo error de proceso, weasyprint generó un PDF
	// de cero bytes, lo cual indica HTML tan roto que no produjo contenido.
	// Mejor detectarlo aquí que devolverle al cliente un PDF corrupto.
	if stdoutBuf.Len() == 0 {
		return nil, fmt.Errorf("weasyprint no produjo contenido; el HTML puede estar vacío o ser inválido")
	}

	return &domain.ConversionResult{PDF: stdoutBuf.Bytes()}, nil
}
