# Etapa 1: compilacion del binario de Go
# golang:1.25-bookworm coincide con la directiva 'go' del go.mod y tiene
# soporte nativo para arm64 (Apple Silicon) y amd64.
FROM golang:1.25-bookworm AS builder

WORKDIR /app

# Copiamos los archivos de módulo primero para aprovechar el cache de capas.
# Si solo cambia el código fuente (no las dependencias), Docker reutiliza la
# capa del 'go mod download' y el build es mucho más rápido.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produce un binario estático que no depende de librerías del SO.
# -ldflags="-s -w" elimina la tabla de símbolos y la info de debug,
# recortando el tamaño del binario ~30% sin afectar el comportamiento en runtime.
# No fijamos GOARCH para que tome la arquitectura de la plataforma donde se construye
# (amd64 en Linux/Windows x86, arm64 en Apple Silicon). El binario resultante
# corre nativamente sin emulación.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/goweasyprint \
    ./cmd/server


# Etapa 2: imagen final de runtime
# Usamos debian:bookworm-slim (Debian 12, stable actual) en lugar de bullseye
# porque bullseye está en EOL y su repositorio de seguridad tiene paquetes
# que ya no se encuentran en los mirrors, lo que rompe el apt-get install.
# bookworm tiene soporte activo, sus paquetes son más recientes y
# mantiene la misma cadena Pango/Cairo/GDK-Pixbuf que necesita WeasyPrint.
FROM debian:bookworm-slim

# Todo en un solo RUN para que Docker cree una única capa y el 'rm -rf'
# del final realmente reduzca el tamaño de la imagen. Si lo separáramos en
# múltiples RUN, la capa de apt-get quedaría guardada aunque borremos los archivos.
#
# Cambios respecto a bullseye:
#   - libffi7 -> libffi8  (renombrado en bookworm)
#   - pip install necesita --break-system-packages (PEP 668, activo desde bookworm)
RUN apt-get update && apt-get install -y --no-install-recommends \
        python3 \
        python3-pip \
        libpango-1.0-0 \
        libpangocairo-1.0-0 \
        libpangoft2-1.0-0 \
        libcairo2 \
        libgdk-pixbuf-2.0-0 \
        libffi8 \
        libxml2 \
        libxslt1.1 \
        fonts-liberation \
        fonts-dejavu-core \
        ca-certificates \
    && pip3 install --no-cache-dir --break-system-packages weasyprint \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Usuario sin privilegios para ejecutar el servidor.
# Si hay una vulnerabilidad de RCE, el atacante obtiene permisos de "appuser"
# en lugar de root. En un contenedor es la primera línea de defensa obvia.
RUN useradd --system --no-create-home --shell /bin/false appuser

WORKDIR /app

# Solo copiamos el binario compilado; nada del código fuente llega a la imagen final.
COPY --from=builder /app/goweasyprint .

RUN chown appuser:appuser /app/goweasyprint

USER appuser

EXPOSE 8080

# Valores por defecto seguros para producción.
# Se pueden sobreescribir con -e al lanzar el contenedor.
ENV PORT=8080
ENV MAX_CONCURRENCY=6
ENV TIMEOUT_SECONDS=60

CMD ["/app/goweasyprint"]
