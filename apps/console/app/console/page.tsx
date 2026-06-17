"use client";

import { useCallback, useEffect, useState } from "react";
import "./console.css";

// ----- types (mirror the /api proxy responses) -----
type Decision = { document_id: string; allowed: boolean; reason: string };
type AuditEntry = {
  trace_id: string;
  timestamp_utc: string;
  user_id: string;
  agent_key_name?: string;
  acl_decision: string;
  reason: string;
  fail_closed: boolean;
  total_latency_ms: number;
  decisions?: Decision[];
};
type AuditResp = { source: string; entries: AuditEntry[]; verify: { verified: boolean; entries_checked: number } };
type Finding = { kind: string; severity: "high" | "medium" | "low"; title: string; detail: string };
type Graph = { teams: string[]; documents: string[]; tuples: number };

const VIEWS = ["overview", "connect", "agent", "audit", "leak"] as const;
type View = (typeof VIEWS)[number];
const META: Record<View, [string, string]> = {
  overview: ["Overview", "Runtime governance at a glance"],
  connect: ["Connect", "Source systems"],
  agent: ["Connect Agent", "Put Groundwork in the path"],
  audit: ["Audit Timeline", "Tamper-evident decision log"],
  leak: ["Leak Report", "Pre-emptive exposure scan"],
};

const PERSONAS = [
  { id: "eve", label: "Eve · Executive" },
  { id: "bob", label: "Bob · Engineering" },
  { id: "alice", label: "Alice · Finance" },
  { id: "carol", label: "Carol · HR" },
  { id: "dave", label: "Dave · Security" },
];

const MCP_CONFIG = `"mcpServers": {
  "groundwork": {
    "url": "https://acme.groundwork.app/mcp",
    "headers": {
      "X-Groundwork-API-Key": "gw_live_••••••",
      "X-Groundwork-User-Assertion": "<SSO-signed JWT>"
    }
  }
}`;

function Mark({ size = 30 }: { size?: number }) {
  return (
    <span className="logo">
      <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
        <rect x="2" y="2" width="28" height="28" rx="8" fill="url(#gwg)" />
        <path d="M9 19.5l7-4 7 4M9 14l7-4 7 4M16 23.5v-8" stroke="#fff" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" />
        <defs>
          <linearGradient id="gwg" x1="2" y1="2" x2="30" y2="30">
            <stop stopColor="#6366f1" />
            <stop offset="1" stopColor="#2dd4bf" />
          </linearGradient>
        </defs>
      </svg>
      <span className="wm">
        <b>Groundwork</b>
      </span>
    </span>
  );
}

export default function ConsolePage() {
  const [signedIn, setSignedIn] = useState(false);
  const [view, setView] = useState<View>("overview");
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [audit, setAudit] = useState<AuditResp | null>(null);
  const [findings, setFindings] = useState<Finding[]>([]);
  const [graph, setGraph] = useState<Graph | null>(null);
  const [pat, setPat] = useState("");
  const [copied, setCopied] = useState(false);
  // live "try it"
  const [persona, setPersona] = useState("bob");
  const [question, setQuestion] = useState("summarize the executive strategy");
  const [tryResult, setTryResult] = useState<string>("");

  const load = useCallback(async () => {
    try {
      const [a, l] = await Promise.all([
        fetch("/api/audit").then((r) => r.json()),
        fetch("/api/leak-report").then((r) => r.json()),
      ]);
      setAudit(a);
      setFindings(l.findings ?? []);
    } catch {
      /* routes always return demo data; ignore */
    }
  }, []);

  useEffect(() => {
    if (signedIn) load();
  }, [signedIn, load]);

  async function connect() {
    const r = await fetch("/api/connect", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pat, org: "acme-financial" }),
    }).then((x) => x.json());
    if (r.graph) setGraph(r.graph);
  }

  async function runQuery() {
    setTryResult("Running…");
    try {
      const r = await fetch("/api/query", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ persona, question }),
      });
      const data = await r.json();
      if (!r.ok) {
        setTryResult(`Runtime not connected — showing demo behavior. (${data.error ?? r.status})`);
        return;
      }
      const n = (data.citations ?? []).length;
      setTryResult(n > 0 ? `ALLOWED — ${n} grounded citation(s) returned` : "BLOCKED — fail-closed, zero content returned");
    } catch {
      setTryResult("Runtime not connected — wire QUERY_RUNTIME_URL to run live.");
    }
  }

  if (!signedIn) {
    return (
      <div id="gw">
        <div className="signin">
          <div className="si-card">
            <Mark size={34} />
            <h1>Runtime authorization for enterprise AI</h1>
            <p>Govern which agent sees what, on whose behalf — with tamper-evident proof.</p>
            <button className="gbtn" onClick={() => setSignedIn(true)}>
              <svg width="17" height="17" viewBox="0 0 24 24">
                <path fill="#4285F4" d="M22.5 12.2c0-.8-.1-1.5-.2-2.2H12v4.3h5.9a5 5 0 0 1-2.2 3.3v2.8h3.6c2.1-2 3.2-4.9 3.2-8.2z" />
                <path fill="#34A853" d="M12 23c2.9 0 5.4-1 7.2-2.6l-3.6-2.8c-1 .7-2.3 1.1-3.6 1.1-2.8 0-5.1-1.9-6-4.4H2.3v2.9A11 11 0 0 0 12 23z" />
                <path fill="#FBBC05" d="M6 14.3a6.6 6.6 0 0 1 0-4.2V7.2H2.3a11 11 0 0 0 0 9.9L6 14.3z" />
                <path fill="#EA4335" d="M12 5.4c1.6 0 3 .5 4.1 1.6l3.1-3.1A11 11 0 0 0 2.3 7.2L6 10.1c.9-2.5 3.2-4.4 6-4.4z" />
              </svg>
              Continue with Google
            </button>
            <div className="si-foot">
              <span className="dot" /> SOC 2 controls in progress · fail-closed by design
            </div>
          </div>
        </div>
      </div>
    );
  }

  const verified = audit?.verify?.verified ?? true;
  const checked = audit?.verify?.entries_checked ?? 0;
  const live = audit?.source === "live";

  return (
    <div id="gw">
      <div className="shell">
        <aside className="side">
          <Mark />
          {VIEWS.map((v) => (
            <button key={v} className={`nav${view === v ? " active" : ""}`} onClick={() => setView(v)}>
              {META[v][0]}
            </button>
          ))}
          <div className="side-foot">
            Tenant <b>acme-financial</b>
            <br />
            Mode <b>ENFORCE · fail-closed</b>
          </div>
        </aside>

        <div>
          <div className="topbar">
            <div>
              <h2>{META[view][0]}</h2>
              <div className="sub">{META[view][1]}</div>
            </div>
            <div className={`pill${live ? "" : " warn"}`}>
              <span className="dot" /> {live ? "Runtime connected · chain verified" : "Demo data · connect runtime for live"}
            </div>
          </div>

          <div className="content">
            {view === "overview" && (
              <div className="view">
                <div className="grid g3" style={{ marginBottom: 16 }}>
                  <div className="card">
                    <div className="label">Queries governed</div>
                    <div className="stat">{audit?.entries.length ?? 0}</div>
                    <p className="dim" style={{ marginTop: 6 }}>recent decisions</p>
                  </div>
                  <div className="card">
                    <div className="label">Blocked / fail-closed</div>
                    <div className="stat r">{audit?.entries.filter((e) => e.acl_decision !== "allowed").length ?? 0}</div>
                    <p className="dim" style={{ marginTop: 6 }}>leakage prevented</p>
                  </div>
                  <div className="card">
                    <div className="label">Chain integrity</div>
                    <div className="stat g">{verified ? "100%" : "FAIL"}</div>
                    <p className="dim" style={{ marginTop: 6 }}>{checked} entries verified</p>
                  </div>
                </div>
                <div className="card" style={{ marginBottom: 16 }}>
                  <p className="kicker">The loop</p>
                  <div className="flow">
                    <div className="flowcol"><div className="hd">Agent</div><div className="node">Claude Desktop<div className="n2">via MCP gateway</div></div></div>
                    <div className="arrowcol">→</div>
                    <div className="flowcol"><div className="hd">Groundwork</div><div className="node" style={{ borderColor: "var(--indigo-deep)" }}>Authorize · Audit · Verify<div className="n2">per-chunk, fail-closed</div></div></div>
                    <div className="arrowcol">→</div>
                    <div className="flowcol"><div className="hd">Enterprise</div><div className="node">GitHub · acme-financial<div className="n2">5 repos · 5 teams</div></div></div>
                  </div>
                </div>
                <div className="card">
                  <p className="kicker">Try it — live enforcement</p>
                  <div style={{ display: "flex", gap: 10, flexWrap: "wrap", alignItems: "center" }}>
                    <select value={persona} onChange={(e) => setPersona(e.target.value)} style={{ maxWidth: 220 }}>
                      {PERSONAS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}
                    </select>
                    <input value={question} onChange={(e) => setQuestion(e.target.value)} style={{ flex: 1, minWidth: 220 }} />
                    <button className="btn" onClick={runQuery}>Ask</button>
                  </div>
                  {tryResult && <p className="dim" style={{ marginTop: 12 }}>{tryResult}</p>}
                </div>
              </div>
            )}

            {view === "connect" && (
              <div className="view">
                <p className="lead">Connect a source system</p>
                <p className="dim" style={{ marginBottom: 22 }}>Read-only. Groundwork ingests entitlements + content; it never writes to your systems.</p>
                <div className="card" style={{ marginBottom: 16 }}>
                  <div className="label" style={{ marginBottom: 10 }}>GitHub organization — read-only PAT</div>
                  <div style={{ display: "flex", gap: 10 }}>
                    <input className="mono" placeholder="ghp_…" value={pat} onChange={(e) => setPat(e.target.value)} />
                    <button className="btn" onClick={connect}>Connect GitHub</button>
                  </div>
                  <p className="dim" style={{ marginTop: 10 }}>Scopes: read:org + repository read. We never request write access.</p>
                </div>
                {graph && (
                  <div className="card">
                    <div className="verify" style={{ background: "linear-gradient(92deg,rgba(99,102,241,.12),rgba(45,212,191,.04))", borderColor: "rgba(99,102,241,.3)" }}>
                      <div className="ic" style={{ background: "rgba(99,102,241,.16)", color: "var(--indigo)" }}>✓</div>
                      <div><b>Synced acme-financial</b> &nbsp;<span>{graph.teams.length} teams → groups · {graph.documents.length} repos → documents · {graph.tuples} tuples written to OpenFGA</span></div>
                    </div>
                    <div className="flow">
                      <div className="flowcol"><div className="hd">Teams → Groups</div>{graph.teams.map((t) => <div key={t} className="node">{t}</div>)}</div>
                      <div className="arrowcol">→</div>
                      <div className="flowcol"><div className="hd">Repos → Documents</div>{graph.documents.map((d) => <div key={d} className="node">{d}</div>)}</div>
                    </div>
                  </div>
                )}
              </div>
            )}

            {view === "agent" && (
              <div className="view">
                <p className="lead">Put Groundwork in your agent&apos;s path</p>
                <p className="dim" style={{ marginBottom: 22 }}>Your agent calls Groundwork as its MCP server. Every retrieval is authorized and audited — without changing the agent.</p>
                <div className="grid g2">
                  <div className="card">
                    <div className="label" style={{ marginBottom: 12 }}>1 · Paste into Claude Desktop config</div>
                    <div className="code">
                      <button className="cp" onClick={() => { navigator.clipboard?.writeText(MCP_CONFIG); setCopied(true); setTimeout(() => setCopied(false), 1500); }}>{copied ? "Copied ✓" : "Copy"}</button>
                      {MCP_CONFIG}
                    </div>
                  </div>
                  <div className="card">
                    <div className="label" style={{ marginBottom: 12 }}>2 · How identity is proven</div>
                    <ol className="steps">
                      <li><b>Agent</b> = the API key Groundwork issued. A credential, never self-reported.</li>
                      <li><b>User</b> = a short-lived JWT signed by your IdP at SSO login.</li>
                      <li><b>Never</b> trusted: anything in the request body.</li>
                      <li>Both are bound into every audit entry, tamper-evidently.</li>
                    </ol>
                  </div>
                </div>
              </div>
            )}

            {view === "audit" && (
              <div className="view">
                <div className="verify">
                  <div className="ic">✓</div>
                  <div><b>{verified ? "Audit chain verified" : "Chain verification FAILED"}</b> &nbsp;<span>{checked} entries · SHA-256 hash-chained{live ? "" : " · demo data"}</span></div>
                  <button className="btn ghost" style={{ marginLeft: "auto" }} onClick={load}>Re-verify chain</button>
                </div>
                {(audit?.entries ?? []).map((r) => {
                  const isOpen = !!open[r.trace_id];
                  const allow = r.acl_decision === "allowed";
                  return (
                    <div key={r.trace_id}>
                      <button className={`row${isOpen ? " open" : ""}`} onClick={() => setOpen((o) => ({ ...o, [r.trace_id]: !o[r.trace_id] }))}>
                        <span className={`badge ${allow ? "allow" : "deny"}`}>{allow ? "ALLOW" : "DENY"}</span>
                        <div>
                          <div className="who">{r.user_id}</div>
                          <div className="meta"><b>agent</b> {r.agent_key_name ?? "—"} &nbsp;·&nbsp; {r.reason}</div>
                        </div>
                        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
                          <div className="t">{new Date(r.timestamp_utc).toLocaleTimeString()}<br />{r.total_latency_ms}ms</div>
                          <span className="chev">›</span>
                        </div>
                      </button>
                      {isOpen && (
                        <div className="decisions">
                          {(r.decisions ?? []).map((d, i) => (
                            <div className="dec" key={i}>
                              <span className={`badge ${d.allowed ? "allow" : "deny"}`}>{d.allowed ? "ALLOW" : "DENY"}</span>
                              <span className="doc">{d.document_id}</span>
                              <span className="rsn">{d.reason}</span>
                            </div>
                          ))}
                          {(r.decisions ?? []).length === 0 && <div className="dec"><span className="rsn">no per-chunk detail recorded</span></div>}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}

            {view === "leak" && (
              <div className="view">
                <p className="lead">Leak Report — acme-financial</p>
                <p className="dim" style={{ marginBottom: 18 }}>What&apos;s overexposed, before any agent even asks.</p>
                <div className="grid g3" style={{ marginBottom: 20 }}>
                  <div className="card"><div className="label">High severity</div><div className="stat r">{findings.filter((f) => f.severity === "high").length}</div></div>
                  <div className="card"><div className="label">Medium</div><div className="stat a">{findings.filter((f) => f.severity === "medium").length}</div></div>
                  <div className="card"><div className="label">Low</div><div className="stat" style={{ color: "var(--muted)" }}>{findings.filter((f) => f.severity === "low").length}</div></div>
                </div>
                {findings.map((f, i) => (
                  <div key={i} className={`finding ${f.severity}`}>
                    <div className="body">
                      <div className="ttl">{f.title}</div>
                      <div className="det" dangerouslySetInnerHTML={{ __html: f.detail }} />
                    </div>
                    <span className={`badge ${f.severity}`}>{f.severity.toUpperCase()}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
