package com.groundwork.sdk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * Per-chunk access decision produced by the runtime's enforcement step. Returned in
 * the trace so the caller (or an auditor) can see exactly which candidates were
 * considered, which were allowed, and which were blocked &mdash; with the reason.
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public final class AccessDecision {

  private final String chunkId;
  private final String documentId;
  private final String outcome;  // "allowed" | "blocked_by_acl" | "blocked_by_residency" | "soft_deleted"
  private final String reason;

  public AccessDecision(
      @JsonProperty("chunk_id") String chunkId,
      @JsonProperty("document_id") String documentId,
      @JsonProperty("outcome") String outcome,
      @JsonProperty("reason") String reason) {
    this.chunkId = chunkId;
    this.documentId = documentId;
    this.outcome = outcome;
    this.reason = reason;
  }

  public String chunkId() { return chunkId; }
  public String documentId() { return documentId; }
  public String outcome() { return outcome; }
  public String reason() { return reason; }
}
