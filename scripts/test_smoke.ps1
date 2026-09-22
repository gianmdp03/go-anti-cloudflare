# PowerShell Smoke Test Script: Go TLS-Spoofing Sidecar Proxy
param(
    [string]$ProxyHost = "localhost",
    [int]$ProxyPort = 8079
)

$ErrorActionPreference = "Stop"
$BaseUrl = "http://${ProxyHost}:${ProxyPort}"

Write-Host "================================================================" -ForegroundColor Cyan
Write-Host "  MGP TLS-Spoofing Sidecar Proxy: Windows Smoke Test Suite      " -ForegroundColor Cyan
Write-Host "================================================================" -ForegroundColor Cyan
Write-Host "Target Base URL: $BaseUrl`n"

# 1. Health Probe Validation
Write-Host "[1/2] Checking Health Probe (/healthz)..." -ForegroundColor Yellow
try {
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $healthResp = Invoke-RestMethod -Uri "$BaseUrl/healthz" -Method Get -TimeoutSec 5
    $stopwatch.Stop()
    $timeMs = [math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)

    Write-Host "[PASS] Health probe responded HTTP 200 in ${timeMs}ms" -ForegroundColor Green
    Write-Host "       Status: $($healthResp.status) | Uptime: $($healthResp.uptime_seconds)s | Go: $($healthResp.go_version) | Profile: $($healthResp.tls_profile)`n"
} catch {
    Write-Host "[FAIL] Could not connect to health probe at $BaseUrl/healthz" -ForegroundColor Red
    Write-Host "       Error: $_"
    exit 1
}

# 2. Proxy Forwarding Test
Write-Host "[2/2] Testing /proxy endpoint forwarding..." -ForegroundColor Yellow
$traceId = "smoke-ps-" + [Guid]::NewGuid().ToString()
$headers = @{
    "Origin" = "http://localhost:8400"
    "Content-Type" = "application/x-www-form-urlencoded"
    "X-Request-ID" = $traceId
}
$body = "accion=RecuperarLineaPorCuandoLlega"

try {
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $response = Invoke-WebRequest -Uri "$BaseUrl/proxy" -Method Post -Headers $headers -Body $body -TimeoutSec 15
    $stopwatch.Stop()
    $timeMs = [math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)

    $content = $response.Content
    Write-Host "       HTTP Status: $($response.StatusCode) | Duration: ${timeMs}ms"

    if ($content -match "Just a moment|cf-turnstile|challenge-platform") {
        Write-Host "[WARN] Cloudflare Managed Challenge screen detected." -ForegroundColor Yellow
        Write-Host "       Remediation: configure PROXY_URL (residential proxy) or CF_CLEARANCE cookie."
        exit 1
    }

    Write-Host "[PASS] Upstream returned HTTP 200 OK without challenge!" -ForegroundColor Green
    $snippet = if ($content.Length -gt 150) { $content.Substring(0, 150) + "..." } else { $content }
    Write-Host "       Response snippet: $snippet"
} catch {
    $ex = $_.Exception
    if ($ex.Response) {
        $statusCode = [int]$ex.Response.StatusCode
        Write-Host "[INFO] HTTP Status: $statusCode" -ForegroundColor Yellow
        $reader = [System.IO.StreamReader]::new($ex.Response.GetResponseStream())
        $errBody = $reader.ReadToEnd()
        if ($errBody -match "Just a moment|cf-turnstile|challenge-platform") {
            Write-Host "[WARN] Cloudflare Managed Challenge triggered for this host IP." -ForegroundColor Yellow
            Write-Host "       Configure PROXY_URL or CF_CLEARANCE in environment."
        }
    } else {
        Write-Host "[FAIL] Request failed: $_" -ForegroundColor Red
        exit 1
    }
}

Write-Host "`n================================================================" -ForegroundColor Cyan
Write-Host "  Smoke Test Complete                                           " -ForegroundColor Cyan
Write-Host "================================================================" -ForegroundColor Cyan
