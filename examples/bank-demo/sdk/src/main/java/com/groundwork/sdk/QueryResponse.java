package com.groundwork.sdk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

import java.util.Collections;
import java.util.List;

/**
 * Parsed response from {@code POST /v1/query}.
 *
 * <p>Two key surfaces:
 * <ul>
 *   <li>{@link #citations()} &mdash; chunks the runtime decided the end user is
 *       authorized to see. May be empty (the fail-closed case).</li>
 *   <li>{@link #trace()} &mdash; the immutable record of the decision, with hash chain
 *       digest, per-chunk access decisions, latency, and decision mode.</li>
 * </ul>
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public final class QueryResponse {

  private final String answer;
  private final List<Citation> citations;
  private final Trace trace;

  public QueryResponse(
      @JsonProperty("answer") String answer,
      @JsonProperty("citations") List<Citation> citations,
      @JsonProperty("trace") Trace trace) {
    this.answer = answer;
    this.citations = citations == null ? Collections.emptyList() : citations;
    this.trace = trace;
  }

  public String answer() { return answer; }
  public List<Citation> citations() { return citations; }
  public Trace trace() { return trace; }
}
