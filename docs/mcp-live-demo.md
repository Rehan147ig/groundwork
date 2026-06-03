# Groundwork MCP Live Demo

This demo proves Groundwork can run as a Model Context Protocol server and enforce document access before an AI agent receives context.

## Run

```powershell
.\scripts\demo-groundwork-mcp.ps1
```

## What It Proves

The script sends four JSON-RPC messages to the Groundwork MCP server over stdio:

1. `initialize`
2. `tools/list`
3. `tools/call` as `finance_user`
4. `tools/call` as `general_user`

The same MCP tool, `groundwork_search`, is called with the same question:

```text
How do live ACL checks fail closed?
```

Expected result:

- `finance_user` receives the permitted `sharepoint-policy` document.
- `general_user` receives zero citations.
- The denied response includes a trace ID and `Blocked by ACL: 1`.

## Why This Matters

This is not a generic RAG benchmark. It is a security-control proof:

```text
AI agent -> MCP tool -> Groundwork engine -> retrieval -> live ACL filter -> permitted context only
```

The key claim is simple:

```text
If the user cannot access the source document, the AI agent cannot receive the chunk.
```

## Claude Desktop MCP Example

For a local stdio MCP client, configure the command to run the query runtime in MCP mode:

```json
{
  "mcpServers": {
    "groundwork": {
      "command": "C:\\Program Files\\Go\\bin\\go.exe",
      "args": ["run", "./cmd/query-runtime"],
      "env": {
        "GROUNDWORK_MCP": "true",
        "ALLOW_MEMORY_API_KEYS": "true",
        "BOOTSTRAP_TENANT_ID": "tenant_demo",
        "BOOTSTRAP_TENANT_REGION": "uk"
      }
    }
  }
}
```

Run the MCP server from:

```text
services/query-runtime
```

For production demos, replace memory mode with a Postgres-backed API key and tenant resolver.
