# Contrato de Interfaz: Go TLS-Spoofing Sidecar Proxy & Spring Boot

Este documento especifica el contrato de comunicación de red, cabeceras, payloads y ejemplos de integración entre el backend **Spring Boot** (que opera en el puerto `8400`) y el **Go TLS-Spoofing Sidecar Proxy** (que opera en el puerto `8080`).

---

## 1. Topología de Red

```text
+-------------------------------------------------------------+
| Red Local / Subnet Docker Bridge Privada                    |
|                                                             |
|   +---------------------+         +---------------------+   |         +---------------------------+
|   |   Spring Boot App   | ------> |  Go Sidecar Proxy   | --+-------> | Upstream MGP (Cloudflare) |
|   |    (:8400)          |  HTTP   |     (:8080)         |   TLS/H2    | appsl.mardelplata.gob.ar  |
|   +---------------------+         +---------------------+   | (JA4)   +---------------------------+
|                                                             |
+-------------------------------------------------------------+
```

---

## 2. Endpoints Expuestos por el Sidecar

### A. Endpoint Proxy (`/proxy`)
Forwardea la petición al upstream municipal eludiendo el control TLS/JA4 de Cloudflare.

- **URL:** `http://localhost:8080/proxy` (o `http://mgp-proxy:8080/proxy` en Docker)
- **Métodos permitidos:** `POST`, `GET`, `OPTIONS`
- **Content-Type requerido:** `application/x-www-form-urlencoded` (o `application/json`)
- **Cabeceras soportadas:**
  - `Origin: http://localhost:8400` (Verificado por middleware CORS)
  - `X-Request-ID: <uuid>` (Opcional; si no se envía, el proxy genera uno y lo propaga)
  - `Accept: application/json, text/javascript, */*; q=0.01`

#### Petición Curl Exacta desde Spring Boot / Host
```bash
curl -i -X POST http://localhost:8080/proxy \
  -H "Origin: http://localhost:8400" \
  -H "Content-Type: application/x-www-form-urlencoded; charset=UTF-8" \
  -H "X-Request-ID: 7f8a9b0c-1234-5678-9abc-def012345678" \
  --data "accion=RecuperarLineaPorCuandoLlega"
```

#### Respuesta Exitosa (HTTP 200 OK)
```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
X-Request-ID: 7f8a9b0c-1234-5678-9abc-def012345678
Date: Sat, 19 Sep 2026 15:58:00 GMT
Transfer-Encoding: chunked

[
  {"id": 1, "descripcion": "511 A"},
  {"id": 2, "descripcion": "512 B"}
]
```

---

### B. Endpoint de Salud (`/healthz`)
Permite a Kubernetes, Docker Swarm, Docker Compose o Spring Boot verificar la disponibilidad y estado del sidecar.

- **URL:** `http://localhost:8080/healthz`
- **Método:** `GET`

#### Petición Curl
```bash
curl -i http://localhost:8080/healthz
```

#### Respuesta
```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8
Cache-Control: no-cache, no-store, must-revalidate

{
  "status": "UP",
  "uptime_seconds": 342.15,
  "timestamp": "2026-09-19T15:58:10Z",
  "go_version": "go1.27.1",
  "tls_profile": "firefox_120"
}
```

---

## 3. Ejemplo de Integración en Spring Boot (Java)

### Opción 1: Spring WebClient (Spring Boot 3.x / WebFlux)
```java
package com.example.transit.client;

import org.springframework.http.MediaType;
import org.springframework.stereotype.Service;
import org.springframework.web.reactive.function.BodyInserters;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.core.publisher.Mono;

import java.util.UUID;

@Service
public class MgpTransitClient {

    private final WebClient webClient;

    public MgpTransitClient(WebClient.Builder builder) {
        // En Docker Compose apuntar a "http://mgp-proxy:8080"
        this.webClient = builder
                .baseUrl("http://localhost:8080")
                .defaultHeader("Origin", "http://localhost:8400")
                .build();
    }

    public Mono<String> obtenerLineas() {
        return webClient.post()
                .uri("/proxy")
                .header("X-Request-ID", UUID.randomUUID().toString())
                .contentType(MediaType.APPLICATION_FORM_URLENCODED)
                .body(BodyInserters.fromFormData("accion", "RecuperarLineaPorCuandoLlega"))
                .retrieve()
                .bodyToMono(String.class);
    }
}
```

### Opción 2: Spring RestClient (Spring Boot 3.2+)
```java
package com.example.transit.client;

import org.springframework.http.MediaType;
import org.springframework.stereotype.Service;
import org.springframework.util.LinkedMultiValueMap;
import org.springframework.util.MultiValueMap;
import org.springframework.web.client.RestClient;

import java.util.UUID;

@Service
public class MgpTransitRestClient {

    private final RestClient restClient;

    public MgpTransitRestClient(RestClient.Builder builder) {
        this.restClient = builder
                .baseUrl("http://localhost:8080")
                .defaultHeader("Origin", "http://localhost:8400")
                .build();
    }

    public String recuperarLineaPorCuandoLlega() {
        MultiValueMap<String, String> formData = new LinkedMultiValueMap<>();
        formData.add("accion", "RecuperarLineaPorCuandoLlega");

        return restClient.post()
                .uri("/proxy")
                .header("X-Request-ID", UUID.randomUUID().toString())
                .contentType(MediaType.APPLICATION_FORM_URLENCODED)
                .body(formData)
                .retrieve()
                .body(String.class);
    }
}
```

---

## 4. Códigos de Error y Diagnóstico

| Código HTTP | Causa | Solución |
|---|---|---|
| `400 Bad Request` | Request body excede 10 MB o payload ilegible | Verificar el tamaño y codificación del body. |
| `403 Forbidden` | Cabecera `Origin` no autorizada por CORS | Enviar `Origin: http://localhost:8400` o configurar `ALLOWED_ORIGIN`. |
| `405 Method Not Allowed` | Método HTTP no soportado (ej. `DELETE`, `PUT`) | Utilizar únicamente `POST` o `GET`. |
| `502 Bad Gateway` | Fallo de conexión o handshake TLS con el upstream | Verificar conectividad a internet o DNS del host. |
| `504 Gateway Timeout` | Upstream excedió el `TIMEOUT_SECONDS` configurado | Aumentar la variable de entorno `TIMEOUT_SECONDS`. |
