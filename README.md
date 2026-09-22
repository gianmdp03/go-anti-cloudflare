# Go TLS-Spoofing Sidecar Proxy (Go 1.27.1)

[![Go Version](https://img.shields.io/badge/Go-1.27.1-00ADD8?style=flat&logo=go)](https://golang.org)
[![Docker Image](https://img.shields.io/badge/Docker-Scratch-blue?logo=docker)](https://hub.docker.com)
[![Memory Footprint](https://img.shields.io/badge/RAM-%3C25MB-brightgreen)](#)
[![License](https://img.shields.io/badge/License-MIT-green)](#)

Servicio sidecar de alto rendimiento en **Go 1.27.1** que opera como proxy inverso de baja latencia contra el upstream municipal de transporte de General Pueyrredon (`https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php`), eludiendo el bloqueo TLS/JA4 de Cloudflare mediante `bogdanfinn/tls-client` y sirviendo peticiones locales para un backend Spring Boot en el puerto `8400`.

---

## Características de Producción

- **TLS / JA4 Fingerprint Spoofing:** Emula fielmente handshakes TLS de navegadores reales (`Firefox_120`, `Firefox_133`, `Chrome_120`, `Chrome_131`) con orden de cabeceras wire-level (`fhttp.HeaderOrderKey`) y cipher suites específicas.
- **Micro-Consumo de Memoria (< 25 MB RAM):** Utiliza `sync.Pool` para buffers de request y streaming directo `io.Copy` para respuestas, eliminando acumulación en heap y presión del recolector de basura.
- **Graceful Shutdown:** Interceptación asíncrona de señales (`SIGINT`, `SIGTERM`) con drenado de conexiones activas en un contexto de 5 segundos.
- **Observabilidad:** Logging estructurado en JSON con la biblioteca estándar `log/slog`, correlación distribuida mediante `X-Request-ID` y métricas de latencia en milisegundos.
- **Health Check Probe:** Endpoint `/healthz` nativo para Kubernetes, Docker Swarm y Docker Compose.
- **Hardening de Seguridad:** Restricción CORS estricta al backend Spring Boot (`ALLOWED_ORIGIN`), protección contra Slowloris y límite de tamaño de body (10 MB).
- **Contenerización Mínima:** Imagen Docker multi-stage basada en `scratch` (~15 MB) ejecutada bajo usuario no privilegiado (`UID 10001`).

---

## Arquitectura de Integración

```text
+-------------------------------------------------------------+
| Red Privada / Docker Bridge Network                         |
|                                                             |
|   +---------------------+         +---------------------+   |         +---------------------------+
|   |   Spring Boot App   | ------> |  Go Sidecar Proxy   | --+-------> | Upstream MGP (Cloudflare) |
|   |    (:8400)          |  HTTP   |     (:8079)         |   TLS/H2    | appsl.mardelplata.gob.ar  |
|   +---------------------+         +---------------------+   | (JA4)   +---------------------------+
|                                                             |
+-------------------------------------------------------------+
```

---

## Estructura del Repositorio

```text
.
├── cmd/
│   ├── server/main.go            # Punto de entrada del servidor, mux, middlewares y ciclo de vida
│   └── healthcheck/main.go       # Sonda de salud estática para imagen scratch
├── internal/
│   ├── config/                   # Configuración inmutable con validación fail-fast
│   ├── middleware/               # Logger (slog), Panic Recovery y Seguridad/CORS
│   └── proxy/                    # Motor de TLS spoofing, pooling y forwarder HTTP
├── deploy/
│   └── mgp-proxy.service         # Unit file systemd para VPS Linux
├── docs/
│   ├── interface_contract.md     # Contrato de interfaz Spring Boot, cURLs y ejemplos Java
│   └── troubleshooting.md        # Guía de comandos y resolución de problemas
├── scripts/
│   ├── test_smoke.sh             # Script de smoke test en bash
│   ├── test_smoke.ps1            # Script de smoke test en PowerShell
│   └── verify_deps.go            # Verificación de toolchain y dependencias
├── Dockerfile                    # Multi-stage build (scratch, non-root)
├── docker-compose.yml            # Orquestación con límites de RAM (32M) y CPUs (0.25)
├── proxy_test.go                 # Suite de integración (Mock E2E y Live Upstream)
└── go.mod
```

---

## Inicio Rápido

### 1. Ejecución Local con Go
```bash
go run cmd/server/main.go
```

### 2. Ejecución con Docker Compose
```bash
docker compose up -d --build
```

### 3. Petición de Prueba desde Spring Boot / Host
```bash
curl -i -X POST http://localhost:8079/proxy \
  -H "Origin: http://localhost:8400" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "X-Request-ID: test-trace-001" \
  --data "accion=RecuperarLineaPorCuandoLlega"
```

### 4. Sonda de Salud
```bash
curl -i http://localhost:8079/healthz
```

---

## Documentación Detallada

- [Contrato de Interfaz y Ejemplos de Spring Boot](docs/interface_contract.md)
- [Guía de Arranque y Troubleshooting](docs/troubleshooting.md)
