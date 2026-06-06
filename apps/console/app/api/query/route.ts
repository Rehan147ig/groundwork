import { NextRequest, NextResponse } from "next/server";
import { SignJWT } from "jose";
import { QueryResponse, validateConsoleQueryRequest } from "@/lib/contracts";

// This proxy is the critical security fix for the console. The runtime's /v1/query is
// wrapped by requireVerifiedIdentity: it expects a cryptographically signed end-user
// assertion in X-Groundwork-User-Assertion. So the proxy MINTS a short-lived HS256 JWT
// whose `sub` is the selected persona, and sends it alongside the tenant API key.
//
//   - tenant/region come from the API key (X-Groundwork-API-Key) — never from the body
//   - end-user identity comes from the signed JWT — never trusted from a plain header
//
// If GROUNDWORK_JWT_HS_SECRET is not set, the proxy falls back to demo mode (sends the
// subject as body user_id), which only works when the runtime runs with
// ALLOW_DEMO_IDENTITY=true. The response is tagged with identity_mode so the UI can warn
// when it's running in the weaker demo path.

export async function POST(request: NextRequest) {
  const payload = await request.json().catch(() => null);
  if (!validateConsoleQueryRequest(payload)) {
    return NextResponse.json({ error: "Invalid Groundwork query payload." }, { status: 400 });
  }

  const subject = (payload.persona ?? payload.user_id ?? "").trim();
  if (!subject) {
    return NextResponse.json({ error: "A persona or user_id is required." }, { status: 400 });
  }

  const runtimeUrl = process.env.QUERY_RUNTIME_URL ?? "http://localhost:8080";
  const apiKey = payload.api_key ?? process.env.GROUNDWORK_API_KEY ?? "";
  if (!apiKey) {
    return NextResponse.json(
      { error: "No API key. Set GROUNDWORK_API_KEY or pass api_key." },
      { status: 400 },
    );
  }
  const secret = process.env.GROUNDWORK_JWT_HS_SECRET ?? "";

  // The body the runtime sees. Never include tenant_id/region (resolved from the key).
  const body: Record<string, unknown> = { question: payload.question };
  if (payload.source_scopes) body.source_scopes = payload.source_scopes;
  if (payload.idk_threshold !== undefined) body.idk_threshold = payload.idk_threshold;

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Groundwork-API-Key": apiKey,
  };

  let identityMode: "verified" | "demo" = "demo";
  if (secret) {
    const token = await new SignJWT({})
      .setProtectedHeader({ alg: "HS256" })
      .setSubject(subject)
      .setIssuedAt()
      .setExpirationTime("10m")
      .sign(new TextEncoder().encode(secret));
    headers["X-Groundwork-User-Assertion"] = token;
    identityMode = "verified";
  } else {
    // Demo fallback: runtime must run with ALLOW_DEMO_IDENTITY=true for this to resolve.
    body.user_id = subject;
  }

  let response: Response;
  try {
    response = await fetch(`${runtimeUrl}/v1/query`, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
      cache: "no-store",
    });
  } catch (error: unknown) {
    return NextResponse.json(
      { error: error instanceof Error ? error.message : "Query runtime unavailable" },
      { status: 502 },
    );
  }

  const parsed = await response.json().catch(() => ({ error: "Runtime returned invalid JSON." }));
  if (!response.ok) {
    return NextResponse.json(parsed, { status: response.status });
  }

  const result = parsed as QueryResponse;
  result.identity_mode = identityMode;
  return NextResponse.json(result);
}
