"use client";

import { FormEvent, useMemo, useState } from "react";
import { Sidebar } from "../Sidebar";
import { QueryResponse } from "@/lib/contracts";

const scopes = [
  { label: "SharePoint", value: "SharePoint" },
  { label: "GoogleDrive", value: "GoogleDrive" },
  { label: "Slack", value: "Slack" },
  { label: "Platform", value: "platform" },
  { label: "Security", value: "security" },
];

export default function LiveAclTestPage() {
  const [apiKey, setApiKey] = useState("gw_local_acme_key");
  const [userId, setUserId] = useState("finance_user");
  const [question, setQuestion] = useState("What is the security policy?");
  const [selectedScopes, setSelectedScopes] = useState<string[]>([]);
  const [result, setResult] = useState<QueryResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const traceJson = useMemo(() => {
    if (!result) return "";
    return JSON.stringify(
      {
        immutable_digest: result.trace.immutable_digest,
        trace_id: result.trace.trace_id,
        decision_mode: result.trace.decision_mode,
        access_decisions: result.trace.access_decisions,
        citations: result.citations.map((citation) => ({
          document_id: citation.document_id,
          chunk_id: citation.chunk_id,
          chunk_hash: citation.chunk_hash,
          score: citation.score,
          freshness_score: citation.freshness_score,
        })),
      },
      null,
      2,
    );
  }, [result]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setLoading(true);
    setError("");
    setResult(null);

    try {
      const response = await fetch("/api/query", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          api_key: apiKey,
          user_id: userId,
          question,
          source_scopes: selectedScopes,
          idk_threshold: 0.1,
        }),
      });
      const payload = await response.json();
      if (!response.ok) throw new Error(payload.error ?? "Query failed");
      setResult(payload);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Query failed");
    } finally {
      setLoading(false);
    }
  }

  function toggleScope(scope: string) {
    setSelectedScopes((current) => (current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope]));
  }

  return (
    <div className="shell">
      <Sidebar />
      <main className="main">
        <div className="page-head">
          <div>
            <div className="eyebrow">Runtime Gateway Simulator</div>
            <h1>Live ACL Test</h1>
            <p className="muted">Send a signed telemetry payload through the console proxy and inspect the Go runtime's fail-closed access decisions.</p>
          </div>
          <span className="status good">/api/query {"->"} /v1/query</span>
        </div>

        <section className="acl-layout">
          <form className="card form-panel" onSubmit={submit}>
            <h2>Simulator form</h2>
            <label>
              <span>api_key</span>
              <input className="input" value={apiKey} onChange={(event) => setApiKey(event.target.value)} required />
            </label>
            <label>
              <span>user_id</span>
              <input className="input" value={userId} onChange={(event) => setUserId(event.target.value)} required />
            </label>
            <label>
              <span>tenant and region</span>
              <input className="input" value="Resolved from API key" disabled />
            </label>
            <label>
              <span>question</span>
              <textarea className="input textarea" value={question} onChange={(event) => setQuestion(event.target.value)} required />
            </label>
            <div>
              <span>source_scopes</span>
              <div className="scope-grid">
                {scopes.map((scope) => (
                  <label className="check" key={scope.value}>
                    <input checked={selectedScopes.includes(scope.value)} onChange={() => toggleScope(scope.value)} type="checkbox" />
                    {scope.label}
                  </label>
                ))}
              </div>
            </div>
            {error ? <p className="error">{error}</p> : null}
            <button className="button" disabled={loading} type="submit">
              {loading ? "Running runtime check..." : "Run live ACL test"}
            </button>
          </form>

          <section className="telemetry-panel">
            <div className="grid telemetry-grid">
              <Diagnostic label="Latency" value={result ? `${result.trace.latency_ms}ms` : "--"} tone="good" />
              <Diagnostic label="Blocked by ACL" value={String(result?.trace.blocked_by_acl ?? 0)} tone={(result?.trace.blocked_by_acl ?? 0) > 0 ? "warn" : "good"} />
              <Diagnostic label="Blocked by Region" value={String(result?.trace.blocked_by_residency ?? 0)} tone={(result?.trace.blocked_by_residency ?? 0) > 0 ? "bad" : "good"} />
              <Diagnostic label="Allowed Citations" value={String(result?.citations.length ?? 0)} tone={(result?.citations.length ?? 0) > 0 ? "good" : "warn"} />
            </div>

            <article className="card" style={{ marginTop: 16 }}>
              <h2>Allowed citations</h2>
              {!result ? <p className="muted">Run a test to see verified chunks.</p> : null}
              <div className="citation-list">
                {result?.citations.map((citation) => (
                  <div className="citation" key={citation.chunk_id}>
                    <strong>{citation.document_id}</strong>
                    <code>{citation.chunk_hash}</code>
                    <p>{citation.text}</p>
                  </div>
                ))}
              </div>
            </article>

            <article className="card" style={{ marginTop: 16 }}>
              <h2>Immutable trace digest</h2>
              <pre className="json-block">{traceJson || "{\n  \"status\": \"waiting_for_runtime_response\"\n}"}</pre>
            </article>
          </section>
        </section>
      </main>
    </div>
  );
}

function Diagnostic({ label, value, tone }: { label: string; value: string; tone: "good" | "warn" | "bad" }) {
  return (
    <article className="card diagnostic">
      <span className={`status ${tone}`}>{label}</span>
      <strong>{value}</strong>
    </article>
  );
}
