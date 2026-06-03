import { HyperAgent } from "@hyperbrowser/agent";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, "..");
const queryRuntimeDir = path.join(repoRoot, "services", "query-runtime");

const agent = new HyperAgent({
  llm: {
    provider: process.env.HYPERAGENT_LLM_PROVIDER || "openai",
    model: process.env.HYPERAGENT_LLM_MODEL || "gpt-4o",
  },
  debug: true,
});

await agent.initializeMCPClient({
  servers: [
    {
      command: process.env.GO_BIN || "go",
      args: ["run", "./cmd/query-runtime"],
      cwd: queryRuntimeDir,
      env: {
        GROUNDWORK_MCP: "true",
        ALLOW_MEMORY_API_KEYS: "true",
        BOOTSTRAP_TENANT_ID: "tenant_demo",
        BOOTSTRAP_TENANT_REGION: "uk",
      },
    },
  ],
});

try {
  const allowed = await agent.executeTask(
    "Use the Groundwork MCP tool to answer this as user_id finance_user: " +
      "How do live ACL checks fail closed? Report the document returned, trace id, and blocked counts."
  );
  console.log("\n=== AUTHORIZED USER ===\n");
  console.log(allowed);

  const denied = await agent.executeTask(
    "Use the Groundwork MCP tool to answer this as user_id general_user: " +
      "How do live ACL checks fail closed? Report whether any document was returned, trace id, and blocked counts."
  );
  console.log("\n=== UNAUTHORIZED USER ===\n");
  console.log(denied);
} finally {
  await agent.closeAgent();
}
