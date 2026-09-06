# Etapa 1: compilacion del binario de Go
# Usamos la imagen oficial de Go sobre Debian para que el binario resultante
# sea compatible con la imagen de runtime que también es Debian.
FROM golang:1.22-bullseye AS builder

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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /app/goweasyprint \
    ./cmd/server


# Etapa 2: imagen final de runtime
# debian:bullseye-slim y no Alpine porque WeasyPrint necesita la cadena
# Pango/Cairo/GDK-Pixbuf. En Alpine esos paquetes requieren parches y builds
# customizados que se rompen con cada actualización de weasyprint.
# Con Debian todo viene probado y empaquetado por el equipo de Debian/Ubuntu.
FROM debian:bullseye-slim

# Todo en un solo RUN para que Docker cree una única capa y el 'rm -rf'
# del final realmente reduzca el tamaño de la imagen. Si lo separáramos en
# múltiples RUN, la capa de apt-get quedaría guardada aunque borremos los archivos.
RUN apt-get update && apt-get install -y --no-install-recommends \
        # Runtime de Python para ejecutar weasyprint
        python3 \
        python3-pip \
        # Cadena de renderizado: sin Pango no hay layout de texto
        libpango-1.0-0 \
        libpangocairo-1.0-0 \
        libpangoft2-1.0-0 \
        # Cairo para el renderizado vectorial del PDF
        libcairo2 \
        # GDK-Pixbuf para imágenes raster (PNG, JPEG) dentro del HTML
        libgdk-pixbuf2.0-0 \
        # Dependencias de Python para los bindings C de cairo y cffi
        libffi7 \
        # Parser XML/HTML y transformaciones XSLT
        libxml2 \
        libxslt1.1 \
        # Fuentes base; sin esto los PDFs generados tienen cuadros en lugar de letras
        fonts-liberation \
        fonts-dejavu-core \
        # Necesario para que weasyprint descargue fuentes externas vía HTTPS
        ca-certificates \
    && pip3 install --no-cache-dir weasyprint \
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
