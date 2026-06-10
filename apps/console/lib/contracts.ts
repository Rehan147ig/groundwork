import citationSchema from "../../../packages/contracts/citation.schema.json";
import querySchema from "../../../packages/contracts/query.schema.json";

export const schemas = {
  citation: citationSchema,
  query: querySchema,
} as const;

export type GroundworkRegion = "us" | "eu" | "uk" | "in";

export type QueryRequest = {
  user_id: string;
  question: string;
  source_scopes?: string[];
  idk_threshold?: number;
};

// The console -> proxy request. Either `persona` (preferred — drives JWT minting) or
// `user_id` (legacy / live-acl-test) identifies the end user. `api_key` is optional;
// when omitted the proxy falls back to the GROUNDWORK_API_KEY env var.
export type ConsoleQueryRequest = {
  persona?: string;
  user_id?: string;
  question: string;
  api_key?: string;
  source_scopes?: string[];
  idk_threshold?: number;
};

export type Citation = {
  document_id: string;
  chunk_id: string;
  chunk_hash: string;
  page: number;
  offset: number;
  text: string;
  score: number;
  freshness_score: number;
};

export type AccessDecision = {
  chunk_id: string;
  chunk_hash: string;
  document_id: string;
  allowed: boolean;
  reason: string;
  region: string;
  required_scope: string;
};

export type RuntimeTrace = {
  trace_id: string;
  tenant_id: string;
  user_id: string;
  region: GroundworkRegion;
  started_at: string;
  latency_ms: number;
  vector_candidates: number;
  keyword_candidates: number;
  blocked_by_acl: number;
  blocked_by_residency: number;
  reranked_candidates: number;
  decision_mode: string;
  access_decisions: AccessDecision[];
  immutable_digest: string;
};

export type QueryResponse = {
  answer: string;
  confidence: number;
  citations: Citation[];
  trace: RuntimeTrace;
  // Added by the console proxy (not part of the runtime contract): which identity path
  // the proxy used. "verified" = signed JWT assertion; "demo" = body user_id fallback.
  identity_mode?: "verified" | "demo";
};

const SHADOW_DECISION_MODE = "engine_shadow_observe";
const ENFORCE_DECISION_MODE = "engine_live_acl_fail_closed";

export function isShadowMode(trace: Pick<RuntimeTrace, "decision_mode">): boolean {
  return trace.decision_mode === SHADOW_DECISION_MODE;
}

export function isEnforcing(trace: Pick<RuntimeTrace, "decision_mode">): boolean {
  return trace.decision_mode === ENFORCE_DECISION_MODE;
}

export function validateConsoleQueryRequest(payload: unknown): payload is ConsoleQueryRequest {
  if (!payload || typeof payload !== "object") return false;
  const value = payload as Partial<ConsoleQueryRequest>;
  const hasSubject =
    (typeof value.persona === "string" && value.persona.length > 0) ||
    (typeof value.user_id === "string" && value.user_id.length > 0);
  return (
    hasSubject &&
    typeof value.question === "string" &&
    value.question.length >= 3 &&
    (value.source_scopes === undefined ||
      (Array.isArray(value.source_scopes) && value.source_scopes.every((scope) => typeof scope === "string"))) &&
    (value.idk_threshold === undefined ||
      (typeof value.idk_threshold === "number" && value.idk_threshold >= 0 && value.idk_threshold <= 1))
  );
}
