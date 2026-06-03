# HyperAgent + Groundwork MCP Demo

Groundwork already has a stdio MCP server. HyperAgent can connect to MCP servers using a `command`, `args`, and `env` configuration, so the fastest live demo is to launch Groundwork directly from HyperAgent.

## What The Demo Shows

Same MCP tool. Same question. Different user identity.

```text
finance_user  -> document returned
general_user  -> zero documents, blocked_by_acl > 0
```

That proves the core Groundwork claim:

```text
If the user cannot access the source document, the AI agent cannot receive the chunk.
```

## Run Locally

Install HyperAgent in the repo, then run:

```powershell
npm install @hyperbrowser/agent
node examples/hyperagent-groundwork-demo.mjs
```

Optional environment:

```powershell
$env:HYPERAGENT_LLM_PROVIDER="openai"
$env:HYPERAGENT_LLM_MODEL="gpt-4o"
$env:GO_BIN="C:\Program Files\Go\bin\go.exe"
```

## Demo File

The runnable example is:

```text
examples/hyperagent-groundwork-demo.mjs
```

It starts Groundwork MCP with:

```js
{
  command: "go",
  args: ["run", "./cmd/query-runtime"],
  cwd: "services/query-runtime",
  env: {
    GROUNDWORK_MCP: "true",
    ALLOW_MEMORY_API_KEYS: "true",
    BOOTSTRAP_TENANT_ID: "tenant_demo",
    BOOTSTRAP_TENANT_REGION: "uk"
  }
}
```

## Production Demo Upgrade

For a stronger enterprise proof, replace memory mode with real services:

```text
DATABASE_URL=postgres://...
OPENFGA_URL=http://...
QDRANT_URL=http://...
ELASTICSEARCH_URL=http://...
EMBEDDING_URL=http://...
```

Then run the same HyperAgent flow. The agent experience stays identical, but the proof uses persistent API keys, OpenFGA, Qdrant, Elasticsearch, and immutable audit logs.

## Why This Is Better Than A Coding Benchmark

Groundwork is not trying to beat coding-agent benchmarks. It is runtime authorization infrastructure.

The benchmark is security behavior:

```text
Can an AI agent retrieve only the chunks the user is allowed to see?
Can Groundwork prove what was blocked and why?
Does the system fail closed?
```

The HyperAgent demo is a live proof of that behavior.
