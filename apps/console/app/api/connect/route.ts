import { NextRequest, NextResponse } from "next/server";

// The Connect step. In V1 the actual org sync is performed by the runtime's
// github-connector (PAT -> teams/repos -> OpenFGA tuples). This route
// records that a connection was requested and returns the resulting
// permission graph for the UI to render. When the runtime later exposes a
// sync endpoint, proxy it here; today it returns the Acme graph so the
// flow is demoable end-to-end. The PAT is never persisted or logged.

const ACME_GRAPH = {
  teams: ["finance-team", "engineering-team", "security-team", "executive-team", "hr-team"],
  documents: [
    "gh:finance-budget",
    "gh:payroll-system",
    "gh:security-audit",
    "gh:executive-strategy",
    "gh:engineering-platform",
  ],
  tuples: 14,
};

export async function POST(request: NextRequest) {
  const body = await request.json().catch(() => ({}));
  const pat = typeof body?.pat === "string" ? body.pat.trim() : "";
  // Validate shape only; never echo or store the token.
  if (pat && !pat.startsWith("ghp_") && !pat.startsWith("github_pat_")) {
    return NextResponse.json({ error: "That does not look like a GitHub PAT." }, { status: 400 });
  }
  const org = typeof body?.org === "string" && body.org.trim() ? body.org.trim() : "acme-financial";
  return NextResponse.json({ source: pat ? "live-requested" : "demo", org, graph: ACME_GRAPH });
}
