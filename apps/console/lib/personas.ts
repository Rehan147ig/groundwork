// Persona registry for the bank-demo console. The ids MUST match the subjects the
// bank-demo seeder writes into OpenFGA (examples/bank-demo/personas/personas.json),
// because the runtime checks "user:<id> viewer document:<id>" against those tuples.
//
// The console mints a signed JWT with sub=<persona id> and sends it as
// X-Groundwork-User-Assertion. The runtime verifies the signature and uses the sub as
// the end-user identity. The persona is therefore NEVER trusted from a plain header —
// it is carried inside a cryptographically signed assertion, exactly like a real IdP.

export type Persona = {
  id: string;
  label: string;
  role: string;
  /** One-line hint shown under the selector — what this persona should and shouldn't see. */
  expectation: string;
  /** Example queries that demonstrate allow/deny for this persona against the demo corpus. */
  examples: string[];
};

export const PERSONAS: Persona[] = [
  {
    id: "teller_jane",
    label: "Jane T. — Junior Teller",
    role: "Junior Teller, London branch",
    expectation: "Sees only all-staff policies. Blocked from credit memos, KYC, audit, and executive material.",
    examples: ["What is our credit policy?", "What is the executive compensation framework?"],
  },
  {
    id: "rm_tony",
    label: "Tony R. — Senior Relationship Manager (London)",
    role: "Senior RM, Corporate Banking, London 01 — handles Stark Industries",
    expectation: "Sees his own clients' credit memos and KYC. Blocked from other RMs' clients and executive-only files.",
    examples: ["Show me the Stark Industries credit memo", "Wayne Enterprises credit assessment"],
  },
  {
    id: "rm_natasha",
    label: "Natasha R. — Relationship Manager (NYC)",
    role: "RM, Corporate Banking, New York 01",
    expectation: "Sees her own portfolio. Must NOT see Tony's Stark Industries files (different RM, different branch).",
    examples: ["Show me the Stark Industries credit memo", "Hammer Industries application"],
  },
  {
    id: "compliance_mhill",
    label: "M. Hill — Compliance Officer (MLRO)",
    role: "Money Laundering Reporting Officer",
    expectation: "Sees KYC packets, AML policy, and adverse-media files across the bank. Blocked from executive comp.",
    examples: ["Hammer Industries adverse media", "What is the AML monitoring policy?"],
  },
  {
    id: "branch_mgr_pepper",
    label: "Pepper P. — Branch Manager (London)",
    role: "Branch Manager London + Head of Corporate Banking",
    expectation: "Sees London branch management files and customer correspondence. Blocked from NYC-only and audit-committee files.",
    examples: ["Stark Industries customer complaint", "London branch credit exposure"],
  },
  {
    id: "auditor_logan",
    label: "Logan — Chief Audit Executive",
    role: "Chief Audit Executive (Internal Audit)",
    expectation: "Sees audit findings, including the audit-committee-only whistleblower file (direct grant).",
    examples: ["loan approval workflow weakness audit finding", "whistleblower follow-up"],
  },
  {
    id: "exec_starkceo",
    label: "Group CEO",
    role: "Group Chief Executive Officer",
    expectation: "Sees executive material and board correspondence. Blocked from the audit-committee-only whistleblower file.",
    examples: ["What is the executive compensation framework?", "whistleblower"],
  },
];

export function personaById(id: string): Persona | undefined {
  return PERSONAS.find((p) => p.id === id);
}
