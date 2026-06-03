import { NextRequest, NextResponse } from "next/server";
import { QueryResponse, validateConsoleQueryRequest } from "@/lib/contracts";

export async function POST(request: NextRequest) {
  const payload = await request.json().catch(() => null);
  if (!validateConsoleQueryRequest(payload)) {
    return NextResponse.json({ error: "Invalid Groundwork query payload." }, { status: 400 });
  }

  const { api_key, ...query } = payload;
  const runtimeUrl = process.env.QUERY_RUNTIME_URL ?? "http://localhost:8080";
  const response = await fetch(`${runtimeUrl}/v1/query`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Authorization": `Bearer ${api_key}`,
    },
    body: JSON.stringify(query),
    cache: "no-store",
  }).catch((error: unknown) => {
    throw new Error(error instanceof Error ? error.message : "Query runtime unavailable");
  });

  const body = await response.json().catch(() => ({ error: "Runtime returned invalid JSON." }));
  if (!response.ok) {
    return NextResponse.json(body, { status: response.status });
  }

  return NextResponse.json(body as QueryResponse);
}
