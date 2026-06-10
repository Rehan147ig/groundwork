"use client";

import { FormEvent, useMemo, useState } from "react";
import { Sidebar } from "../Sidebar";
import { PERSONAS, personaById } from "@/lib/personas";
import { AccessDecision, QueryResponse, isShadowMode } from "@/lib/contracts";

export default function DemoConsolePage() {
  const [personaId, setPersonaId] = useState(PERSONAS[0].id);
  const [question, setQuestion] = useState(PERSONAS[0].examples[0]);
  const [result, setResult] = useState<QueryResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const persona = personaById(personaId)!;

  const blocked = useMemo<AccessDecision[]>(
    () => (result?.trace.access_decisions ?? []).filter((d) => !d.allowed),
    [result],
  );
  const shadow = result ? isShadowMode(result.trace) : false;

  const traceJson = useMemo(() => {
    if (!result) return "";
    return JSON.stringify(
      {
        trace_id: result.trace.trace_id,
        immutable_digest: result.trace.immutable_digest,
        decision_mode: result.trace.decision_mode,
        blocked_by_acl: result.trace.blocked_by_acl,
        access_decisions: result.trace.access_decisions,
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
        body: JSON.stringify({ persona: personaId, question, idk_threshold: 0.1 }),
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

  function pickPersona(id: string) {
    setPersonaId(id);
    const p = personaById(id);
    if (p) setQuestion(p.examples[0]);
    setResult(null);
    setError("");
  }

  return (
    <div className="shell">
      <Sidebar />
      <main className="main">
        <div className="page-head">
          <div>
            <div className="eyebrow">Runtime Authorization — Live</div>
            <h1>Demo Console</h1>
            <p className="muted">
              Ask a question as a specific bank persona. Groundwork verifies identity, checks every
              candidate chunk against the permission graph, and returns only what the persona may see.
            </p>
          </div>
          {result?.identity_mode ? (
            <span className={`status ${result.identity_mode === "verified" ? "good" : "warn"}`}>
              identity: {result.identity_mode === "verified" ? "verified JWT" : "demo mode"}
            </span>
          ) : null}
        </div>

        <section className="acl-layout">
          <form className="card form-panel" onSubmit={submit}>
            <h2>Ask as a persona</h2>
            <label>
              <span>persona</span>
              <select className="input" value={personaId} onChange={(e) => pickPersona(e.target.value)}>
                {PERSONAS.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label}
                  </option>
                ))}
              </select>
            </label>
            <p className="persona-meta">
              <strong>{persona.role}.</strong> {persona.expectation}
            </p>

            <label>
              <span>question</span>
              <textarea
                className="input textarea"
                value={question}
                onChange={(e) => setQuestion(e.target.value)}
                required
              />
            </label>
            <div className="chips">
              {persona.examples.map((ex) => (
                <button type="button" key={ex} className="chip" onClick={() => setQuestion(ex)}>
                  {ex}
                </button>
              ))}
            </div>

            {error ? <p className="error">{error}</p> : null}
            <button className="button" disabled={loading} type="submit">
              {loading ? "Checking permissions…" : "Run query through Groundwork"}
            </button>
            <p className="muted fineprint">
              The console mints a signed JWT (<code>sub = persona</code>) sent as
              <code> X-Groundwork-User-Assertion</code>. Tenant comes from the API key. The persona is
              never trusted from a plain header.
            </p>
          </form>

          <section className="telemetry-panel">
            {result ? (
              <div className={`mode-banner ${shadow ? "shadow" : "enforce"}`}>
                <strong>{shadow ? "SHADOW MODE — observing only" : "ENFORCING"}</strong>
                <span>
                  {shadow
                    ? "Chunks are returned, but Groundwork records what it would have blocked."
                    : "Unauthorized chunks are stripped before the model sees them."}
                </span>
              </div>
            ) : null}

            <div className="grid telemetry-grid">
              <Diagnostic label="Latency" value={result ? `${result.trace.latency_ms}ms` : "--"} tone="good" />
              <Diagnostic
                label="Allowed citations"
                value={String(result?.citations.length ?? 0)}
                tone={(result?.citations.length ?? 0) > 0 ? "good" : "warn"}
              />
              <Diagnostic
                label={shadow ? "Would block" : "Blocked by ACL"}
                value={String(shadow ? blocked.length : result?.trace.blocked_by_acl ?? 0)}
                tone={(shadow ? blocked.length : result?.trace.blocked_by_acl ?? 0) > 0 ? (shadow ? "warn" : "good") : "good"}
              />
              <Diagnostic label="Trace" value={result ? result.trace.trace_id.slice(0, 10) : "--"} tone="good" />
            </div>

            {shadow && blocked.length > 0 ? (
              <div className="shadow-callout">
                <strong>{blocked.length} shadow violation{blocked.length > 1 ? "s" : ""}.</strong> These chunks
                were returned to the user but would be blocked under enforcement. This is the leak report a CISO
                runs before flipping the switch.
              </div>
            ) : null}

            <article className="card" style={{ marginTop: 16 }}>
              <h2>Allowed citations</h2>
              {!result ? <p className="muted">Run a query to see what this persona may retrieve.</p> : null}
              {result && result.citations.length === 0 ? (
                <p className="muted">No documents returned — this persona is not authorized for anything matching the query.</p>
              ) : null}
              <div className="citation-list">
                {result?.citations.map((c) => (
                  <div className="citation" key={c.chunk_id}>
                    <strong>{c.document_id}</strong>
                    <code>{c.chunk_hash.slice(0, 24)}</code>
                    <p>{c.text}</p>
                  </div>
                ))}
              </div>
            </article>

            <article className="card" style={{ marginTop: 16 }}>
              <h2>{shadow ? "Would-block decisions" : "Blocked chunks"}</h2>
              {!result ? <p className="muted">Per-chunk access decisions appear here.</p> : null}
              {result && blocked.length === 0 ? (
                <p className="muted">No chunks were denied for this query.</p>
              ) : null}
              <div className="blocked-list">
                {blocked.map((d) => (
                  <div className="blocked-item" key={d.chunk_id}>
                    <div className="blocked-head">
                      <strong>{d.document_id}</strong>
                      <span className="status bad">denied</span>
                    </div>
                    <p className="muted">{d.reason || "not authorized"}{d.required_scope ? ` · requires ${d.required_scope}` : ""}</p>
                  </div>
                ))}
              </div>
            </article>

            <article className="card" style={{ marginTop: 16 }}>
              <h2>Immutable trace</h2>
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
