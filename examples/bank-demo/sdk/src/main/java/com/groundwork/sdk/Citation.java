package com.groundwork.sdk;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;

/**
 * A document chunk that the runtime authorized for return to the caller. Includes the
 * chunk's hash, freshness signal, similarity score, and the source document identifier.
 */
@JsonIgnoreProperties(ignoreUnknown = true)
public final class Citation {

  private final String documentId;
  private final String chunkId;
  private final String chunkHash;
  private final String text;
  private final Double score;
  private final Double freshnessScore;

  public Citation(
      @JsonProperty("document_id") String documentId,
      @JsonProperty("chunk_id") String chunkId,
      @JsonProperty("chunk_hash") String chunkHash,
      @JsonProperty("text") String text,
      @JsonProperty("score") Double score,
      @JsonProperty("freshness_score") Double freshnessScore) {
    this.documentId = documentId;
    this.chunkId = chunkId;
    this.chunkHash = chunkHash;
    this.text = text;
    this.score = score;
    this.freshnessScore = freshnessScore;
  }

  public String documentId() { return documentId; }
  public String chunkId() { return chunkId; }
  public String chunkHash() { return chunkHash; }
  public String text() { return text; }
  public Double score() { return score; }
  public Double freshnessScore() { return freshnessScore; }
}
