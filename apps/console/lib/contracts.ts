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

export type ConsoleQueryRequest = QueryRequest & {
  api_key: string;
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
};

export function validateQueryRequest(payload: unknown): payload is QueryRequest {
  if (!payload || typeof payload !== "object") return false;
  const value = payload as Partial<QueryRequest>;
  return (
    typeof value.user_id === "string" &&
    value.user_id.length > 0 &&
    typeof value.question === "string" &&
    value.question.length >= 3 &&
    (value.source_scopes === undefined || (Array.isArray(value.source_scopes) && value.source_scopes.every((scope) => typeof scope === "string"))) &&
    (value.idk_threshold === undefined || (typeof value.idk_threshold === "number" && value.idk_threshold >= 0 && value.idk_threshold <= 1))
  );
}

export function validateConsoleQueryRequest(payload: unknown): payload is ConsoleQueryRequest {
  if (!payload || typeof payload !== "object") return false;
  const value = payload as Partial<ConsoleQueryRequest>;
  return typeof value.api_key === "string" && value.api_key.length > 0 && validateQueryRequest(value);
}
