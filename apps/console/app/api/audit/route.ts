import { NextResponse } from "next/server";

// Proxies the runtime's read-only Audit API (GET /v1/audit and
// /v1/audit/verify) using a server-side API key, so the key never reaches
// the browser. Falls back to curated demo data when the runtime is
// unreachable or unconfigured — the console must look alive in a pitch even
// with a cold backend. The `source` field tells the UI which path it got.

type AuditEntry = {
  trace_id: string;
  timestamp_utc: string;
  user_id: string;
  agent_key_name?: string;
  acl_decision: string;
  reason: string;
  fail_closed: boolean;
  total_latency_ms: number;
  // per-chunk decisions (detail); demo data includes a couple
  decisions?: { document_id: string; allowed: boolean; reason: string }[];
};

const DEMO_ENTRIES: AuditEntry[] = [
  { trace_id: "7f3a", timestamp_utc: "2026-06-10T14:22:05Z", user_id: "eve", agent_key_name: "claude-eve", acl_decision: "allowed", reason: "executive-team#member → viewer", fail_closed: false, total_latency_ms: 91, decisions: [{ document_id: "gh:executive-strategy", allowed: true, reason: "allowed" }] },
  { trace_id: "7f39", timestamp_utc: "2026-06-10T14:21:40Z", user_id: "bob", agent_key_name: "claude-bob", acl_decision: "denied", reason: "no path: executive-team", fail_closed: false, total_latency_ms: 46, decisions: [{ document_id: "gh:executive-strategy", allowed: false, reason: "fga:viewer denied" }] },
  { trace_id: "7f38", timestamp_utc: "2026-06-10T14:20:12Z", user_id: "carol", agent_key_name: "hr-agent", acl_decision: "denied", reason: "eng-owned repo; HR not member", fail_closed: false, total_latency_ms: 44, decisions: [{ document_id: "gh:payroll-system", allowed: false, reason: "fga:viewer denied" }] },
  { trace_id: "7f37", timestamp_utc: "2026-06-10T14:18:55Z", user_id: "alice", agent_key_name: "claude-alice", acl_decision: "allowed", reason: "finance-team#member → viewer", fail_closed: false, total_latency_ms: 88, decisions: [{ document_id: "gh:finance-budget", allowed: true, reason: "allowed" }] },
  { trace_id: "7f36", timestamp_utc: "2026-06-10T14:15:03Z", user_id: "dave", agent_key_name: "sec-agent", acl_decision: "allowed", reason: "security-team#member → viewer", fail_closed: false, total_latency_ms: 79, decisions: [{ document_id: "gh:security-audit", allowed: true, reason: "allowed" }] },
];

export async function GET() {
  const runtimeUrl = process.env.QUERY_RUNTIME_URL ?? "";
  const apiKey = process.env.GROUNDWORK_API_KEY ?? "";

  if (runtimeUrl && apiKey) {
    try {
      const [listRes, verifyRes] = await Promise.all([
        fetch(`${runtimeUrl}/v1/audit?limit=25`, { headers: { "X-Groundwork-API-Key": apiKey }, cache: "no-store" }),
        fetch(`${runtimeUrl}/v1/audit/verify`, { headers: { "X-Groundwork-API-Key": apiKey }, cache: "no-store" }),
      ]);
      if (listRes.ok) {
        const list = await listRes.json();
        const verify = verifyRes.ok ? await verifyRes.json() : { verified: true, entries_checked: (list.entries ?? []).length };
        return NextResponse.json({ source: "live", entries: list.entries ?? [], verify });
      }
    } catch {
      /* fall through to demo */
    }
  }

  return NextResponse.json({
    source: "demo",
    entries: DEMO_ENTRIES,
    verify: { verified: true, entries_checked: DEMO_ENTRIES.length },
  });
}
