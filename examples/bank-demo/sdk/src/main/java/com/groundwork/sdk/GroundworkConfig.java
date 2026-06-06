package com.groundwork.sdk;

import java.time.Duration;
import java.util.Objects;

/**
 * Configuration for a {@link GroundworkClient}. Immutable; build via the static
 * {@link #builder()} method.
 *
 * <p>Required fields:
 * <ul>
 *   <li>{@code runtimeBaseUrl} &mdash; URL of the Groundwork query-runtime, e.g.
 *       {@code https://groundwork.bank.internal:8080}.</li>
 *   <li>{@code apiKey} &mdash; tenant API key issued via the runtime's admin API. The
 *       runtime resolves the calling tenant and region from this key; the client never
 *       supplies them in the body (and the runtime would ignore them if it did).</li>
 * </ul>
 *
 * <p>Optional:
 * <ul>
 *   <li>{@code requestTimeout} &mdash; per-request timeout, default 5 seconds.</li>
 * </ul>
 */
public final class GroundworkConfig {

  private final String runtimeBaseUrl;
  private final String apiKey;
  private final Duration requestTimeout;

  private GroundworkConfig(Builder b) {
    this.runtimeBaseUrl = Objects.requireNonNull(b.runtimeBaseUrl, "runtimeBaseUrl is required");
    this.apiKey = Objects.requireNonNull(b.apiKey, "apiKey is required");
    this.requestTimeout = b.requestTimeout != null ? b.requestTimeout : Duration.ofSeconds(5);
  }

  public String runtimeBaseUrl() { return runtimeBaseUrl; }
  public String apiKey() { return apiKey; }
  public Duration requestTimeout() { return requestTimeout; }

  public static Builder builder() { return new Builder(); }

  public static final class Builder {
    private String runtimeBaseUrl;
    private String apiKey;
    private Duration requestTimeout;

    public Builder runtimeBaseUrl(String url) { this.runtimeBaseUrl = url; return this; }
    public Builder apiKey(String key) { this.apiKey = key; return this; }
    public Builder requestTimeout(Duration timeout) { this.requestTimeout = timeout; return this; }

    public GroundworkConfig build() { return new GroundworkConfig(this); }
  }
}
