package com.groundwork.sdk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.Collections;
import java.util.List;

/**
 * Immutable trace of a single Groundwork query. Carries:
 *
 * <ul>
 *   <li>{@code traceId} &mdash; opaque identifier for the query.</li>
 *   <li>{@code immutableDigest} &mdash; the trace's position in the tamper-evident hash
 *       chain.</li>
 *   <li>{@code decisionMode} &mdash; "enforce" or "shadow"; in shadow mode the runtime
 *       returns the chunks but also records the access decisions that <em>would have
 *       blocked</em> the query under enforcement.</li>
 *   <li>{@code accessDecisions} &mdash; per-chunk evidence of how each candidate was
 *       handled (allowed / blocked by ACL / blocked by residency / soft-deleted).</li>
 *   <li>{@code blockedByAcl}, {@code blockedByResidency} &mdash; counters for the same
 *       facts, surfaced for dashboards.</li>
 *   <li>{@code latencyMs} &mdash; runtime budget consumed.</li>
 * </ul>
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public final class Trace {

  private final String traceId;
  private final String immutableDigest;
  private final String decisionMode;
  private final List<AccessDecision> accessDecisions;
  private final long blockedByAcl;
  private final long blockedByResidency;
  private final long latencyMs;

  public Trace(
      @JsonProperty("trace_id") String traceId,
      @JsonProperty("immutable_digest") String immutableDigest,
      @JsonProperty("decision_mode") String decisionMode,
      @JsonProperty("access_decisions") List<AccessDecision> accessDecisions,
      @JsonProperty("blocked_by_acl") long blockedByAcl,
      @JsonProperty("blocked_by_residency") long blockedByResidency,
      @JsonProperty("latency_ms") long latencyMs) {
    this.traceId = traceId;
    this.immutableDigest = immutableDigest;
    this.decisionMode = decisionMode;
    this.accessDecisions = accessDecisions == null ? Collections.emptyList() : accessDecisions;
    this.blockedByAcl = blockedByAcl;
    this.blockedByResidency = blockedByResidency;
    this.latencyMs = latencyMs;
  }

  public String traceId() { return traceId; }
  public String immutableDigest() { return immutableDigest; }
  public String decisionMode() { return decisionMode; }
  public List<AccessDecision> accessDecisions() { return accessDecisions; }
  public long blockedByAcl() { return blockedByAcl; }
  public long blockedByResidency() { return blockedByResidency; }
  public long latencyMs() { return latencyMs; }
}
