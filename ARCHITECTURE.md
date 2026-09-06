# Arquitectura de GoWeasyPDF

## Visión general

GoWeasyPDF sigue los principios de **Clean Architecture**: las capas internas no conocen a las externas, la dependencia fluye hacia adentro, y todo lo que puede cambiar (el conversor, el transporte HTTP) está detrás de una interfaz.

```
┌─────────────────────────────────────────────────────────┐
│                    Delivery (HTTP)                      │
│         Middleware (x-request-id, timeout 60s)          │
│                   Handler POST /generator               │
└───────────────────────┬─────────────────────────────────┘
                        │ llama a
┌───────────────────────▼─────────────────────────────────┐
│                    Use Case Layer                       │
│          PDFGenerationUseCase + Semáforo               │
└───────────────────────┬─────────────────────────────────┘
                        │ llama a (via interfaz)
┌───────────────────────▼─────────────────────────────────┐
│                 Infrastructure Layer                    │
│           WeasyprintConverter (os/exec)                │
└─────────────────────────────────────────────────────────┘
                        │ invoca
┌───────────────────────▼─────────────────────────────────┐
│            Sistema Operativo                            │
│       Subproceso efímero: `weasyprint - -`              │
└─────────────────────────────────────────────────────────┘
```

---

## Patrón de aislamiento de subprocesos

La decisión de diseño más importante del proyecto es **no mantener ningún proceso de navegador en segundo plano**. La razón es práctica: en un contenedor con ~1 GB de RAM, un Chromium o un Firefox corriendo de fondo puede consumir entre 300 y 600 MB antes de recibir la primera petición, dejando muy poco margen para las conversiones reales.

En cambio, cada conversión hace esto:

```
petición HTTP
     │
     ▼
cmd := exec.CommandContext(ctx, "weasyprint", "-", "-")
cmd.Stdin  = bytes.NewReader(html)   // HTML entra por STDIN
cmd.Stdout = &pdfBuffer             // PDF sale por STDOUT
cmd.Stderr = &errBuffer             // errores van a un buffer separado

err = cmd.Run()  // bloquea hasta que el subproceso termina
                 // el SO libera la RAM del subproceso en este punto
```

WeasyPrint acepta `-` como argumento tanto para entrada como para salida, lo que significa que podemos hacer toda la transferencia a través de pipes del SO sin tocar el disco. Esto es importante en un contenedor donde el filesystem puede ser lento o efímero.

### Por qué `exec.CommandContext` y no `exec.Command`

`exec.CommandContext` recibe un `context.Context`. Cuando el contexto se cancela (por timeout o por que el cliente se fue), Go llama a `cmd.Process.Kill()` automáticamente. Sin eso, un proceso de `weasyprint` colgado podría quedarse indefinidamente consumiendo RAM y bloqueando un slot del semáforo.

---

## Patrón Semáforo para control de concurrencia

WeasyPrint carga fuentes, parsea CSS y renderiza en memoria. En documentos grandes puede llegar a usar 150-200 MB por conversión. Con 6 conversiones simultáneas estamos en el límite superior seguro para un contenedor de 1 GB.

El semáforo se implementa con un canal con buffer de Go, que es la forma idiomática de hacerlo:

```go
// El canal funciona como un pool de tokens. Para "adquirir" el semáforo
// se manda un struct vacío; para "liberarlo" se lee del canal.
// struct{} tiene tamaño cero, así que el canal solo cuesta el overhead
// del propio canal, no el de los datos.
semaphore := make(chan struct{}, maxConcurrency)

// Adquirir slot
select {
case semaphore <- struct{}{}:
    defer func() { <-semaphore }()
case <-ctx.Done():
    return nil, ctx.Err()  // el cliente no quiere esperar más
}
```

El `select` con `ctx.Done()` es crítico: si hay 6 conversiones en curso y llega la séptima, en lugar de bloquearse infinitamente esperando un slot libre, el goroutine espera hasta que su contexto expire (60s de timeout total de la petición) y devuelve un error al cliente de inmediato. Esto previene que las peticiones se acumulen en memoria sin límite.

### Por qué un canal y no `sync.Mutex` o `semaphore.Weighted`

Un canal con buffer es la primitiva más directa de Go para este patrón: es legible, testeable y no requiere dependencias externas. `semaphore.Weighted` de `golang.org/x/sync` sería una alternativa válida para semáforos ponderados (donde distintas operaciones "pesan" distinto), pero aquí todas las conversiones tienen el mismo costo, así que el canal es suficiente.

---

## Flujo completo de una petición

```
Cliente
  │
  │  POST /generator  {"html_base64": "..."}
  ▼
Middleware x-request-id
  │  Lee o genera el ID, lo inyecta en el context y en el header de respuesta
  ▼
Middleware Timeout
  │  Crea un context con deadline de 60s. Si el handler no responde a tiempo,
  │  el context se cancela y el subproceso de weasyprint muere con él.
  ▼
Handler
  │  Decodifica Base64 → []byte HTML
  ▼
PDFGenerationUseCase.Execute()
  │  Intenta adquirir slot del semáforo (bloquea hasta slot libre o timeout)
  ▼
WeasyprintConverter.Convert()
  │  Lanza subproceso, escribe HTML en STDIN, lee PDF de STDOUT
  │  El subproceso muere aquí, la RAM se libera
  ▼
Handler
  │  Codifica PDF en Base64, devuelve JSON
  ▼
Cliente
     {"pdf_base64": "..."}
```

---

## Estructura de carpetas

```
GoWeasyPDF/
├── cmd/
│   └── server/
│       └── main.go              # punto de entrada, wiring de dependencias
├── internal/
│   ├── domain/
│   │   └── pdf.go               # interfaces y entidades (sin dependencias externas)
│   ├── usecase/
│   │   └── pdf_usecase.go       # lógica de negocio + semáforo
│   ├── infra/
│   │   └── weasyprint/
│   │       └── converter.go     # invocación del CLI via os/exec
│   └── delivery/
│       └── http/
│           ├── handler.go       # POST /generator
│           ├── middleware.go    # x-request-id + timeout
│           └── router.go        # registro de rutas
├── Dockerfile
├── go.mod
├── .gitignore
├── README.md
└── ARCHITECTURE.md
```

---

## Decisiones de Dockerfile

Usamos `debian:bullseye-slim` como imagen base en lugar de Alpine porque WeasyPrint depende de la cadena de renderizado de Pango/Cairo/GDK-Pixbuf, que en Alpine requiere parches y compilaciones customizadas que son frágiles de mantener. Debian tiene todos esos paquetes probados y disponibles con `apt-get`, lo que hace el build reproducible y predecible.
