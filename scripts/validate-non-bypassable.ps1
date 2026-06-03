<#
.SYNOPSIS
  Validates the Groundwork non-bypassable deployment profile.

.DESCRIPTION
  Checks that:
    - query-runtime is reachable through the gateway
    - /mcp is reachable (and requires an API key)
    - Qdrant / OpenFGA / PostgreSQL / Elasticsearch are NOT reachable on host ports
    - (optional) an authenticated query still works through /mcp

  Requires PowerShell 7+ (uses -SkipHttpErrorCheck). Exit code 0 = only Groundwork
  ingress is exposed; non-zero = a check failed.

.EXAMPLE
  ./scripts/validate-non-bypassable.ps1 -GwUrl http://localhost
  $env:GW_API_KEY="gw_live_xxx"; ./scripts/validate-non-bypassable.ps1
#>
param(
    [string]$GwUrl  = "http://localhost",
    [string]$ApiKey = $env:GW_API_KEY
)

$script:fail = $false
function Pass($m) { Write-Host "PASS: $m" }
function Bad($m)  { Write-Host "FAIL: $m"; $script:fail = $true }

function Get-HttpCode {
    param([string]$Method, [string]$Url, [hashtable]$Headers, [string]$Body)
    try {
        $p = @{ Method = $Method; Uri = $Url; TimeoutSec = 5; SkipHttpErrorCheck = $true }
        if ($Headers) { $p.Headers = $Headers }
        if ($Body)    { $p.Body = $Body; $p.ContentType = 'application/json' }
        return (Invoke-WebRequest @p).StatusCode
    } catch { return -1 }
}

Write-Host "== Groundwork non-bypassable validation =="
Write-Host "gateway: $GwUrl`n"

# 1. query-runtime reachable through the gateway.
$c = Get-HttpCode -Method GET -Url "$GwUrl/healthz"
if ($c -eq 200) { Pass "query-runtime /healthz reachable (200)" } else { Bad "/healthz expected 200, got $c" }

# 2. /mcp reachable AND auth-protected (401 without an API key).
$c = Get-HttpCode -Method POST -Url "$GwUrl/mcp" -Body '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
if ($c -eq 401) { Pass "/mcp reachable and requires API key (401)" } else { Bad "/mcp expected 401 without key, got $c" }

# 3-6. Backend host ports MUST be closed.
function Test-Closed {
    param([int]$Port, [string]$Name)
    $r = Test-NetConnection -ComputerName 127.0.0.1 -Port $Port -WarningAction SilentlyContinue
    if ($r.TcpTestSucceeded) { Bad "$Name ($Port) is reachable from the host — it must be internal-only" }
    else { Pass "$Name ($Port) not reachable from the host" }
}
Test-Closed 6333 "Qdrant"
Test-Closed 8081 "OpenFGA"
Test-Closed 5432 "PostgreSQL"
Test-Closed 9200 "Elasticsearch"

# 7. Optional: a real authenticated query still works through /mcp.
if ($ApiKey) {
    try {
        $resp = Invoke-WebRequest -Method POST -Uri "$GwUrl/mcp" `
            -Headers @{ 'X-Groundwork-API-Key' = $ApiKey } -ContentType 'application/json' `
            -Body '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' `
            -TimeoutSec 8 -SkipHttpErrorCheck
        if ($resp.Content -match 'groundwork_search') { Pass "authenticated /mcp tools/list works (Groundwork query path is live)" }
        else { Bad "authenticated /mcp tools/list failed: $($resp.Content)" }
    } catch { Bad "authenticated /mcp call error: $_" }
} else {
    Write-Host "INFO: set GW_API_KEY to also verify an authenticated query through /mcp"
}

Write-Host ""
if (-not $script:fail) {
    Write-Host "ALL CHECKS PASSED — only Groundwork ingress is exposed."
    exit 0
} else {
    Write-Host "CHECKS FAILED — a backend is exposed or Groundwork ingress is broken."
    exit 1
}
