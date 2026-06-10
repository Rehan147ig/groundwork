package com.groundwork.sdk;

import com.fasterxml.jackson.annotation.JsonInclude;
import java.util.List;

/**
 * Wire payload for {@code POST /v1/query}.
 *
 * <p>Note: the request intentionally does <em>not</em> carry {@code tenant_id},
 * {@code region}, or {@code user_id}. These are derived by the runtime from the
 * API key and the verified user assertion respectively. Any such fields sent in the
 * body are ignored.
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
public final class QueryRequest {

  private final String question;
  private final List<String> sourceScopes;
  private final Double idkThreshold;

  private QueryRequest(String question, List<String> sourceScopes, Double idkThreshold) {
    this.question = question;
    this.sourceScopes = sourceScopes;
    this.idkThreshold = idkThreshold;
  }

  public static QueryRequest of(String question) {
    return new QueryRequest(question, null, null);
  }

  public static QueryRequest of(String question, List<String> sourceScopes) {
    return new QueryRequest(question, sourceScopes, null);
  }

  public String getQuestion() { return question; }
  public List<String> getSourceScopes() { return sourceScopes; }
  public Double getIdkThreshold() { return idkThreshold; }
}
