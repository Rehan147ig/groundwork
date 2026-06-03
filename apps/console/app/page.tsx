import Link from "next/link";
import { Activity, DatabaseZap, LockKeyhole, ShieldCheck } from "lucide-react";

const metrics = [
  ["p95 latency", "184ms", "Under 250ms runtime budget", "good"],
  ["ACL fail-closed", "17", "Chunks blocked in last 24h", "warn"],
  ["Trace coverage", "100%", "Every query has immutable digest", "good"],
  ["Region drift", "0", "No tenant crossed residency boundary", "good"],
] as const;

const regions = [
  ["US", "us-central1", "Active", "Customer data constrained to US namespaces"],
  ["EU", "europe-west1", "Ready", "EU tenants isolated from UK and US indexes"],
  ["UK", "europe-west2", "Active", "UK tenants pinned to London region"],
] as const;

const traces = [
  ["qry_7f2a", "Live ACL check excluded 3 SharePoint chunks before prompt assembly.", "good"],
  ["qry_91bc", "Microsoft Graph ACL timeout. Runtime failed closed for 6 candidate chunks.", "warn"],
  ["qry_ab20", "Structured citation response emitted with 7 verified chunk hashes.", "good"],
] as const;

export default function ConsoleHome() {
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">Groundwork</div>
        <nav className="nav">
          <Link href="/">Security Overview</Link>
          <Link href="/live-acl-test">Live ACL Test</Link>
          <Link href="/tenants">Tenants</Link>
          <Link href="/connectors">Connectors</Link>
          <Link href="/traces">Query Traces</Link>
          <Link href="/policies">Policies</Link>
        </nav>
      </aside>
      <main className="main">
        <div className="page-head">
          <div>
            <div className="eyebrow">AI Runtime Control Platform</div>
            <h1>CISO security telemetry</h1>
            <p className="muted">Live ACL interception, fail-closed retrieval, region isolation, and immutable query evidence for enterprise AI.</p>
          </div>
          <Link className="button" href="/live-acl-test">Run ACL test</Link>
        </div>

        <section className="grid metrics">
          {metrics.map(([label, value, note, tone]) => (
            <article className="card metric" key={label}>
              <span className={`status ${tone}`}>{label}</span>
              <strong>{value}</strong>
              <p className="muted">{note}</p>
            </article>
          ))}
        </section>

        <section className="grid two">
          <article className="card">
            <h2>Runtime enforcement</h2>
            <div className="grid" style={{ marginTop: 16 }}>
              <Feature icon={<ShieldCheck size={22} />} title="Live ACL interceptor" body="Runtime checks identity permissions at query time and excludes chunks when the source ACL state cannot be proven." />
              <Feature icon={<LockKeyhole size={22} />} title="Fail-closed prompt assembly" body="Unauthorized, stale, soft-deleted, or region-invalid chunks never reach the model context window." />
              <Feature icon={<Activity size={22} />} title="Immutable query traces" body="Every search, ACL decision, selected chunk, model call, latency budget, and cost signal is logged with a digest." />
              <Feature icon={<DatabaseZap size={22} />} title="Dual-index retrieval" body="Qdrant vector search and Elasticsearch BM25 run in parallel before RRF fusion and reranking." />
            </div>
          </article>

          <article className="card">
            <h2>Residency posture</h2>
            <div className="region-grid" style={{ marginTop: 16 }}>
              {regions.map(([name, zone, status, detail]) => (
                <div className="region" key={name}>
                  <span className="status good">{status}</span>
                  <h3 style={{ marginTop: 10 }}>{name}</h3>
                  <p className="muted">{zone}</p>
                  <p>{detail}</p>
                </div>
              ))}
            </div>
          </article>
        </section>

        <section className="card" style={{ marginTop: 16 }}>
          <h2>Recent security traces</h2>
          <div className="grid" style={{ marginTop: 16 }}>
            {traces.map(([id, detail, tone]) => (
              <div className="trace" key={id}>
                <strong>{id}</strong>
                <span className={`status ${tone}`}>{tone === "good" ? "verified" : "fail-closed"}</span>
                <p className="muted">{detail}</p>
              </div>
            ))}
          </div>
        </section>
      </main>
    </div>
  );
}

function Feature({ icon, title, body }: { icon: React.ReactNode; title: string; body: string }) {
  return (
    <div style={{ display: "grid", gap: 10, gridTemplateColumns: "32px minmax(0, 1fr)" }}>
      <div style={{ color: "var(--teal)" }}>{icon}</div>
      <div>
        <h3>{title}</h3>
        <p className="muted" style={{ marginTop: 4 }}>{body}</p>
      </div>
    </div>
  );
}
