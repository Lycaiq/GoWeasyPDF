# Makefile para GoWeasyPDF
# No es obligatorio, pero evita tener que recordar los flags exactos de cada comando.
# `make` sin argumentos muestra la ayuda.

.DEFAULT_GOAL := help

IMAGE_NAME  := goweasyprint
IMAGE_TAG   := local
BINARY_NAME := goweasyprint

.PHONY: help build run test lint docker-build docker-run clean

help: ## Muestra esta ayuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Compila el binario localmente
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_NAME) ./cmd/server

run: build ## Compila y ejecuta el servidor en local
	./$(BINARY_NAME)

test: ## Ejecuta todos los tests de integracion
	go test ./... -v -timeout 120s -count=1

lint: ## Ejecuta go vet sobre todos los paquetes
	go vet ./...

docker-build: ## Construye la imagen Docker
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) .

docker-run: ## Ejecuta el contenedor con la configuracion por defecto
	docker run --rm -p 8080:8080 \
		-e MAX_CONCURRENCY=6 \
		-e TIMEOUT_SECONDS=60 \
		$(IMAGE_NAME):$(IMAGE_TAG)

clean: ## Elimina el binario local
	rm -f $(BINARY_NAME)
