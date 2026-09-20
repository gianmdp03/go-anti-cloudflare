# Guía Rápida de Arranque y Troubleshooting

Esta guía detalla los comandos de ejecución para entornos locales, Docker, Docker Compose y systemd, junto con el manual de diagnóstico y solución de incidencias operativas para el **Go TLS-Spoofing Sidecar Proxy**.

---

## 1. Comandos de Arranque Rápido

### A. Ejecución Local Directa (Go 1.27.1)
```bash
# Descargar y verificar dependencias
go mod download
go run scripts/verify_deps.go

# Ejecutar el servidor con variables por defecto
go run cmd/server/main.go
```

Con variables de entorno personalizadas:
```bash
PORT=8080 \
ALLOWED_ORIGIN=http://localhost:8400 \
TLS_PROFILE=Firefox_120 \
LOG_LEVEL=debug \
go run cmd/server/main.go
```

En Windows (PowerShell):
```powershell
$env:PORT="8080"
$env:ALLOWED_ORIGIN="http://localhost:8400"
$env:TLS_PROFILE="Firefox_120"
go run cmd/server/main.go
```

---

### B. Contenedor Docker Individual (Multi-Stage Scratch)
```bash
# Compilar imagen de producción (tamaño aproximado: ~15 MB)
docker build -t mgp-proxy:latest .

# Ejecutar con límite de memoria de 32 MB
docker run -d \
  --name mgp-proxy \
  -p 8080:8080 \
  -m 32m \
  --cpus 0.25 \
  -e ALLOWED_ORIGIN="http://localhost:8400" \
  -e LOG_LEVEL="info" \
  mgp-proxy:latest

# Inspeccionar logs en formato JSON
docker logs -f mgp-proxy
```

---

### C. Despliegue con Docker Compose (Sidecar + Spring Boot)
```bash
# Levantar proxy y mock/backend de Spring Boot
docker compose up -d --build

# Verificar estado y healthcheck
docker compose ps

# Monitorear consumo de RAM y CPU en tiempo real (< 25 MB objetivo)
docker stats mgp-proxy

# Detener servicios
docker compose down
```

---

### D. Despliegue en Servidor VPS Linux con Systemd
```bash
# 1. Compilar binario estático para Linux
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o /usr/local/bin/mgp-proxy ./cmd/server

# 2. Crear usuario de servicio dedicado
sudo useradd -r -s /bin/false mgpproxy
sudo mkdir -p /var/run/mgp-proxy
sudo chown mgpproxy:mgpproxy /var/run/mgp-proxy

# 3. Instalar y habilitar servicio systemd
sudo cp deploy/mgp-proxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now mgp-proxy

# 4. Verificar estado y logs
sudo systemctl status mgp-proxy
sudo journalctl -u mgp-proxy -f -o json-pretty
```

---

## 2. Variables de Entorno

| Variable | Tipo | Default | Descripción |
|---|---|---|---|
| `PORT` | int | `8080` | Puerto TCP de escucha del servidor HTTP. |
| `UPSTREAM_URL` | string | `https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php` | URL del endpoint de transporte de General Pueyrredon. |
| `TIMEOUT_SECONDS` | int | `15` | Timeout máximo para peticiones salientes al upstream. |
| `ALLOWED_ORIGIN` | string | `http://localhost:8400` | Origen CORS autorizado (backend Spring Boot). |
| `TLS_PROFILE` | string | `Firefox_120` | Perfil de fingerprint TLS (`Firefox_120`, `Firefox_133`, `Chrome_120`, `Chrome_131`). |
| `LOG_LEVEL` | string | `info` | Nivel de logging estructurado (`debug`, `info`, `warn`, `error`). |
| `PROXY_URL` | string | `""` | (Opcional) Proxy residencial saliente (`http://user:pass@host:port` o `socks5://host:port`). |
| `DEFAULT_COOKIE` | string | `""` | (Opcional) Cookie por defecto a inyectar (ej. `cf_clearance=...`). |
| `CF_CLEARANCE` | string | `""` | (Opcional) Token `cf_clearance` a inyectar automáticamente. |

---

## 3. Pruebas de Validación y Smoke Testing

### Ejecutar Suite Completa de Tests Unitarios e Integración
```bash
go test -v ./...
```

### Ejecutar Script de Smoke Test en Linux/macOS
```bash
chmod +x scripts/test_smoke.sh
./scripts/test_smoke.sh
```

### Ejecutar Script de Smoke Test en Windows (PowerShell)
```powershell
powershell -ExecutionPolicy Bypass -File scripts/test_smoke.ps1
```

---

## 4. Troubleshooting: Guía de Resolución de Problemas

### 1. Pantalla "Just a moment..." o HTTP 403 de Cloudflare
* **Síntoma:** El upstream devuelve código `403 Forbidden` con HTML conteniendo `Just a moment...` o `cf-turnstile`.
* **Causa:** La IP pública desde la que se origina la petición tiene baja reputación en el sistema Cloudflare Bot Management (frecuente en rangos de IP de datacenters o ISPs residenciales durante modo "Under Attack").
* **Soluciones:**
  1. **Rotación mediante Proxy Residencial:** Configurar `PROXY_URL` con un proxy residencial argentino o proxy rotativo:
     ```bash
     export PROXY_URL="http://usuario:password@proxy-residencial.com:8000"
     ```
  2. **Inyección de Cookie `cf_clearance`:** Si el backend o un solver resuelve el reto de Cloudflare, puede enviarse la cabecera `Cookie: cf_clearance=...` directamente en el request de Spring Boot o configurar:
     ```bash
     export CF_CLEARANCE="VALOR_DEL_TOKEN_OBTENIDO"
     ```
     *El proxy preservará el fingerprint JA4 exacto correspondiente a `TLS_PROFILE` con el que se emitió el token.*
  3. **Rotación de Perfil TLS:** Cambiar el perfil de spoofing:
     ```bash
     export TLS_PROFILE="Chrome_131"
     # o
     export TLS_PROFILE="Firefox_133"
     ```

---

### 2. Error `403 Forbidden` por CORS
* **Síntoma:** Spring Boot o el navegador reciben `{"error":"CORS: Origin not allowed"}`.
* **Causa:** La cabecera `Origin` enviada por el cliente no coincide con `ALLOWED_ORIGIN`.
* **Solución:** Ajustar la variable de entorno `ALLOWED_ORIGIN` para que coincida exactamente con la URL de Spring Boot:
  ```bash
  export ALLOWED_ORIGIN="http://localhost:8400"
  ```
  *Nota: Las llamadas directas server-to-server sin cabecera `Origin` (ej. `WebClient` por defecto) son permitidas automáticamente.*

---

### 3. Error `502 Bad Gateway`
* **Síntoma:** La respuesta es `{"error":"Bad Gateway - TLS handshake or network failure"}`.
* **Causa:** Fallo de resolución DNS o pérdida de conectividad a internet en el host/contenedor.
* **Diagnóstico:**
  - Probar conectividad al upstream: `curl -I https://appsl.mardelplata.gob.ar/app_cuando_llega/`
  - Revisar los logs en formato JSON: `docker logs mgp-proxy | grep error`

---

### 4. Error `504 Gateway Timeout`
* **Síntoma:** La respuesta es `{"error":"Upstream Gateway Timeout"}` tras cumplirse el tiempo límite.
* **Causa:** El servidor municipal de General Pueyrredon está saturado y tarda más de `TIMEOUT_SECONDS` en responder.
* **Solución:** Incrementar el timeout a 30 o 45 segundos:
  ```bash
  export TIMEOUT_SECONDS=30
  ```

---

### 5. Reinicios por Límite de Memoria (OOMKilled)
* **Síntoma:** El contenedor de Docker se reinicia con código `137`.
* **Diagnóstico:** Verificar el límite en `docker-compose.yml` (`memory: 32M`).
* **Verificación:** Gracias a la implementación con `sync.Pool` y streaming de socket con `io.Copy`, el consumo nominal del proceso es de tan solo **8 a 14 MB RAM**. Si se envían payloads anormales de más de 10 MB, el middleware los rechaza con `413 Request Entity Too Large` antes de comprometer la memoria del proceso.
