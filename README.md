# GoWeasyPDF

Microservicio en Go para convertir HTML a PDF de alta fidelidad, diseñado para operar bajo recursos limitados (~1 GB RAM) con concurrencia controlada y procesos efímeros.

## Cómo funciona (en dos líneas)

Cada petición HTTP desencadena un subproceso de `weasyprint` que vive exactamente lo que dura la conversión y muere al terminar. No hay ningún navegador corriendo en segundo plano. La memoria se libera sola.

---

## Requisitos previos

- **Go** 1.22+
- **WeasyPrint** instalado en el sistema (`pip install weasyprint` o vía paquete del SO)
- Dependencias de sistema para WeasyPrint: `libpango`, `libcairo`, `libgdk-pixbuf` (ver `Dockerfile` para la lista completa en Debian)

```bash
# Verificar que weasyprint está disponible
weasyprint --version
```

---

## Levantar en local

```bash
# Clonar y entrar al directorio
git clone https://github.com/arminbash/goweasyprint.git
cd goweasyprint

# Descargar dependencias de Go
go mod tidy

# Compilar
go build -o goweasyprint ./cmd/server

# Ejecutar (puerto por defecto: 8080)
./goweasyprint
```

Variables de entorno opcionales:

| Variable            | Default | Descripción                                    |
|---------------------|---------|------------------------------------------------|
| `PORT`              | `8080`  | Puerto en el que escucha el servidor           |
| `MAX_CONCURRENCY`   | `6`     | Máximo de conversiones simultáneas (semáforo)  |
| `TIMEOUT_SECONDS`   | `60`    | Timeout por petición en segundos               |

---

## Uso del API

### `POST /generator`

Convierte un documento HTML a PDF. El HTML debe venir codificado en Base64.

**Request:**

```http
POST /generator HTTP/1.1
Content-Type: application/json
x-request-id: mi-id-opcional-para-trazabilidad

{
  "html_base64": "<html_en_base64>"
}
```

**Response exitosa (`200 OK`):**

```json
{
  "pdf_base64": "<pdf_en_base64>"
}
```

**Response de error (`4xx` / `5xx`):**

```json
{
  "error": "descripción del error"
}
```

**Ejemplo rápido con `curl`:**

```bash
# Codificar el HTML
HTML_B64=$(echo '<html><body><h1>Hola mundo</h1></body></html>' | base64)

# Llamar al API
curl -s -X POST http://localhost:8080/generator \
  -H "Content-Type: application/json" \
  -d "{\"html_base64\": \"$HTML_B64\"}" | jq .
```

**Decodificar el PDF resultante:**

```bash
curl -s -X POST http://localhost:8080/generator \
  -H "Content-Type: application/json" \
  -d "{\"html_base64\": \"$HTML_B64\"}" \
  | jq -r '.pdf_base64' \
  | base64 --decode > resultado.pdf
```

---

## Levantar con Docker

```bash
# Construir imagen
docker build -t goweasyprint:local .

# Ejecutar
docker run -p 8080:8080 \
  -e MAX_CONCURRENCY=6 \
  -e TIMEOUT_SECONDS=60 \
  goweasyprint:local
```

---

## Ejecutar los tests

```bash
# Tests de integración (requieren que weasyprint esté instalado)
go test ./... -v -timeout 120s
```

---

## Trazabilidad

Cada petición genera o propaga un header `x-request-id`. Si el cliente no lo envía, el servidor genera uno. Este ID aparece en todos los logs estructurados de la petición, lo que facilita correlacionar logs en producción sin agregar ningún agente externo.

---

## Licencia

MIT
