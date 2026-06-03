param(
    [string]$TenantId = "tenant_demo",
    [string]$Region = "uk",
    [string]$Question = "How do live ACL checks fail closed?"
)

$ErrorActionPreference = "Continue"

$repoRoot = Split-Path -Parent $PSScriptRoot
$runtimeDir = Join-Path $repoRoot "services/query-runtime"

$env:GROUNDWORK_MCP = "true"
$env:ALLOW_MEMORY_API_KEYS = "true"
$env:BOOTSTRAP_TENANT_ID = $TenantId
$env:BOOTSTRAP_TENANT_REGION = $Region

$messages = @(
    @{ jsonrpc = "2.0"; id = 1; method = "initialize"; params = @{} },
    @{ jsonrpc = "2.0"; id = 2; method = "tools/list"; params = @{} },
    @{
        jsonrpc = "2.0"
        id = 3
        method = "tools/call"
        params = @{
            name = "groundwork_search"
            arguments = @{
                user_id = "finance_user"
                question = $Question
            }
        }
    },
    @{
        jsonrpc = "2.0"
        id = 4
        method = "tools/call"
        params = @{
            name = "groundwork_search"
            arguments = @{
                user_id = "general_user"
                question = $Question
            }
        }
    }
) | ForEach-Object { $_ | ConvertTo-Json -Depth 10 -Compress }

Push-Location $runtimeDir
try {
    $messages | & "C:\Program Files\Go\bin\go.exe" run ./cmd/query-runtime 2>$null
}
finally {
    Pop-Location
}
