import { NextResponse } from "next/server";

// Leak Report. The analysis lives in the runtime (internal/leakreport, run
// today via cmd/leak-report). When the runtime exposes GET /v1/leak-report,
// proxy it here. Until then this returns the curated Acme findings — the
// same ones the leakreport package produces for the mock org — so the view
// is demoable. Wiring the live endpoint is a one-step follow-up (a Go
// handler that runs github.Connector.Snapshot -> leakreport.Analyze).

type Finding = { kind: string; severity: "high" | "medium" | "low"; title: string; detail: string };

const DEMO_FINDINGS: Finding[] = [
  { kind: "cross_department_access", severity: "high", title: "Cross-department access", detail: "<code>engineering-team</code> can view <code>gh:finance-budget</code>, which is owned by <code>finance-team</code>." },
  { kind: "excessive_group_access", severity: "high", title: "Excessive group access", detail: "<code>engineering-team</code> can read documents owned by 2 other departments." },
  { kind: "overexposed_document", severity: "medium", title: "Overexposed document", detail: "<code>gh:finance-budget</code> is viewable by 2 groups: finance-team, engineering-team." },
];

export async function GET() {
  const runtimeUrl = process.env.QUERY_RUNTIME_URL ?? "";
  const apiKey = process.env.GROUNDWORK_API_KEY ?? "";
  if (runtimeUrl && apiKey) {
    try {
      // Live: runtime runs github.Connector.Snapshot -> leakreport.Analyze.
      const res = await fetch(`${runtimeUrl}/v1/leak-report`, {
        headers: { "X-Groundwork-API-Key": apiKey },
        cache: "no-store",
      });
      if (res.ok) {
        const data = await res.json();
        const findings: Finding[] = (data.findings ?? []).map((f: Record<string, string>) => ({
          kind: f.kind,
          severity: (f.severity as Finding["severity"]) ?? "low",
          title: f.title ?? f.kind,
          detail: f.detail ?? "",
        }));
        return NextResponse.json({ source: "live", findings });
      }
    } catch {
      /* fall through to demo */
    }
  }
  return NextResponse.json({ source: "demo", findings: DEMO_FINDINGS });
}
