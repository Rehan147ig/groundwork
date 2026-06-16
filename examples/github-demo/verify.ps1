$ErrorActionPreference = "Stop"
$RT = "http://localhost:8080"
$KEY = "gw_local_demo_bank_key"
$pass = 0
$fail = 0

function Check-Persona {
    param([string]$persona, [string]$q, [string]$expectDoc, [string]$expect)
    try {
        $body = @{ question = $q; user_id = $persona } | ConvertTo-Json -Compress
        $res = Invoke-RestMethod -Uri "$RT/v1/query" -Method Post -Headers @{"X-Groundwork-API-Key"=$KEY; "Content-Type"="application/json"} -Body $body -ErrorAction Stop
        
        $n = 0
        $foundDoc = $false
        if ($res.citations -ne $null) {
            $n = $res.citations.Length
            foreach ($doc in $res.citations) {
                if ($doc.document_id -eq $expectDoc) {
                    $foundDoc = $true
                    break
                }
            }
        }

        $got = "deny"
        if ($foundDoc) { $got = "allow" }

        if ($got -eq $expect) {
            Write-Host "PASS   $persona | `"$q`" -> $got ($n docs, found $expectDoc=$foundDoc)" -ForegroundColor Green
            $script:pass++
        } else {
            Write-Host "FAIL   $persona | `"$q`" -> $got ($n docs, found $expectDoc=$foundDoc), expected $expect" -ForegroundColor Red
            $script:fail++
        }
    } catch {
        Write-Host "ERROR  $persona | `"$q`" (request failed: $_)" -ForegroundColor Red
        $script:fail++
    }
}

Write-Host "=== Groundwork github-demo acceptance ($RT) ==="
Check-Persona -persona "alice" -q "Q4 budget projections" -expectDoc "gh:finance-budget" -expect "allow"
Check-Persona -persona "bob" -q "executive strategy documents" -expectDoc "gh:executive-strategy" -expect "deny"
Check-Persona -persona "dave" -q "security audit findings" -expectDoc "gh:security-audit" -expect "allow"
Check-Persona -persona "carol" -q "payroll system architecture" -expectDoc "gh:payroll-system" -expect "deny"
Check-Persona -persona "eve" -q "board strategy deck" -expectDoc "gh:executive-strategy" -expect "allow"
Check-Persona -persona "alice" -q "engineering platform docs" -expectDoc "gh:engineering-platform" -expect "deny"
Check-Persona -persona "bob" -q "payroll system code" -expectDoc "gh:payroll-system" -expect "allow"
Check-Persona -persona "carol" -q "Q4 budget details" -expectDoc "gh:finance-budget" -expect "deny"
Check-Persona -persona "dave" -q "executive compensation" -expectDoc "gh:executive-strategy" -expect "deny"
Check-Persona -persona "eve" -q "security posture review" -expectDoc "gh:security-audit" -expect "deny"

Write-Host "---"
Write-Host "PASS=$pass  FAIL=$fail"
if ($fail -eq 0) {
    Write-Host "ALL GREEN" -ForegroundColor Green
} else {
    Write-Host "SOME FAILED" -ForegroundColor Red
}
