import Link from "next/link";

// Shared sidebar nav. Replaces the per-page duplicated nav. Only links to routes that
// actually exist — the old dead links (/tenants, /connectors, /traces, /policies) were
// removed as part of the Dashboard Layer 1 cleanup. /traces returns when the audit read
// API (Layer 2) lands.
const LINKS: { href: string; label: string }[] = [
  { href: "/", label: "Security Overview" },
  { href: "/demo", label: "Demo Console" },
  { href: "/live-acl-test", label: "Live ACL Test" },
];

export function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="brand">Groundwork</div>
      <nav className="nav">
        {LINKS.map((link) => (
          <Link key={link.href} href={link.href}>
            {link.label}
          </Link>
        ))}
      </nav>
    </aside>
  );
}
