#!/usr/bin/env bash
# ==============================================================================
# Smoke Test Script: Go TLS-Spoofing Sidecar Proxy
# ==============================================================================
set -euo pipefail

PROXY_HOST="${PROXY_HOST:-localhost}"
PROXY_PORT="${PROXY_PORT:-8080}"
BASE_URL="http://${PROXY_HOST}:${PROXY_PORT}"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}================================================================${NC}"
echo -e "${BLUE}  MGP TLS-Spoofing Sidecar Proxy: Production Smoke Test Suite   ${NC}"
echo -e "${BLUE}================================================================${NC}"
echo -e "Target Base URL: ${BASE_URL}\n"

# ------------------------------------------------------------------------------
# 1. Health Probe Validation (/healthz)
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[1/2] Checking Health Probe (/healthz)...${NC}"
HEALTH_RESPONSE=$(curl -s -w "\n%{http_code}\n%{time_total}" "${BASE_URL}/healthz" || true)

if [ -z "$HEALTH_RESPONSE" ]; then
  echo -e "${RED}[FAIL] Could not connect to proxy server at ${BASE_URL}/healthz.${NC}"
  echo -e "       Ensure the proxy service is running ('go run cmd/server/main.go' or 'docker compose up')."
  exit 1
fi

HEALTH_BODY=$(echo "$HEALTH_RESPONSE" | sed '$d' | sed '$d')
HEALTH_CODE=$(echo "$HEALTH_RESPONSE" | tail -n 2 | head -n 1)
HEALTH_TIME=$(echo "$HEALTH_RESPONSE" | tail -n 1)

if [ "$HEALTH_CODE" -ne 200 ]; then
  echo -e "${RED}[FAIL] Health probe returned HTTP ${HEALTH_CODE}${NC}"
  echo "$HEALTH_BODY"
  exit 1
fi

echo -e "${GREEN}[PASS] Health probe responded HTTP 200 in ${HEALTH_TIME}s${NC}"
echo -e "       Payload: ${HEALTH_BODY}\n"

# ------------------------------------------------------------------------------
# 2. Proxy Forwarding & Anti-Cloudflare Smoke Test (/proxy)
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[2/2] Testing /proxy endpoint forwarding (accion=RecuperarLineaPorCuandoLlega)...${NC}"

TRACE_ID="smoke-$(date +%s)-$RANDOM"
TEMP_RESP=$(mktemp)
HTTP_CODE=$(curl -s -o "$TEMP_RESP" -w "%{http_code}\n%{time_total}" \
  -X POST "${BASE_URL}/proxy" \
  -H "Origin: http://localhost:8400" \
  -H "Content-Type: application/x-www-form-urlencoded; charset=UTF-8" \
  -H "X-Request-ID: ${TRACE_ID}" \
  --data "accion=RecuperarLineaPorCuandoLlega" || true)

STATUS_CODE=$(echo "$HTTP_CODE" | head -n 1)
TIME_TOTAL=$(echo "$HTTP_CODE" | tail -n 1)
BODY=$(cat "$TEMP_RESP")
rm -f "$TEMP_RESP"

echo -e "       HTTP Status: ${STATUS_CODE} | Duration: ${TIME_TOTAL}s"

# Check for Cloudflare Turnstile / Managed Challenge screens
if echo "$BODY" | grep -qE "Just a moment|cf-turnstile|challenge-platform|Attention Required"; then
  echo -e "${RED}[WARN] Cloudflare Managed Challenge screen was returned by upstream.${NC}"
  echo -e "       This occurs if the outgoing IP address has a low Bot Management reputation."
  echo -e "       Remediation: configure a residential proxy via PROXY_URL or inject a CF_CLEARANCE cookie."
  exit 1
fi

if [ "$STATUS_CODE" -eq 200 ]; then
  echo -e "${GREEN}[PASS] Upstream returned HTTP 200 OK without Cloudflare challenge!${NC}"
  SAMPLE=$(echo "$BODY" | cut -c 1-200)
  echo -e "       Response snippet: ${SAMPLE}..."
elif [ "$STATUS_CODE" -eq 403 ]; then
  echo -e "${YELLOW}[WARN] Upstream returned HTTP 403 Forbidden.${NC}"
  SAMPLE=$(echo "$BODY" | cut -c 1-150)
  echo -e "       Response: ${SAMPLE}..."
  exit 1
else
  echo -e "${RED}[FAIL] Upstream returned unexpected HTTP status ${STATUS_CODE}.${NC}"
  echo -e "       Body: ${BODY}"
  exit 1
fi

echo -e "\n${GREEN}================================================================${NC}"
echo -e "${GREEN}  All smoke test assertions PASSED successfully!               ${NC}"
echo -e "${GREEN}================================================================${NC}"
exit 0
