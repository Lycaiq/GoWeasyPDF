// Package http implementa la capa de entrega: transforma peticiones HTTP
// en llamadas al caso de uso y viceversa. No tiene lógica de negocio propia;
// su única responsabilidad es el protocolo.
package http

import (
	"encoding/json"
	"net/http"
)

// writeJSON es un helper interno para no repetir la misma secuencia de
// headers + encode en cada handler. Pequeño, pero evita que alguien olvide
// el Content-Type en un momento de prisa.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Si el encode falla aquí ya enviamos el status code, no podemos corregirlo.
	// En la práctica solo falla si body contiene tipos no serializables (canales,
	// funciones), lo que sería un bug nuestro, no del cliente.
	_ = json.NewEncoder(w).Encode(body)
}

// errorResponse es la estructura canónica de error que devolvemos al cliente.
// Un solo campo "error" es suficiente; no necesitamos código de error interno
// si los status HTTP son descriptivos.
type errorResponse struct {
	Error string `json:"error"`
}
