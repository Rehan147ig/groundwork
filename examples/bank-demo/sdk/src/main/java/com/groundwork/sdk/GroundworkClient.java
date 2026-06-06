package com.groundwork.sdk;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.PropertyNamingStrategies;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.Map;
import java.util.Objects;

/**
 * Java client for the Groundwork runtime authorization layer.
 *
 * <p>The client posts to {@code POST /v1/query} with two headers that together carry the
 * full authentication contract Groundwork enforces:
 *
 * <ul>
 *   <li>{@code X-Groundwork-API-Key} &mdash; identifies the calling tenant. Resolved from
 *       the configured {@link GroundworkConfig#apiKey()}.</li>
 *   <li>{@code X-Groundwork-User-Assertion} &mdash; a signed JWT identifying the end user
 *       on whose behalf the query is being executed. Verified by Groundwork; the
 *       end-user identity is then used to enforce per-document permissions.</li>
 * </ul>
 *
 * <p>This class is wire-compatible with the Groundwork REST API documented in
 * {@code docs/architecture.md}. It is also wire-compatible with the public schema of
 * the Temenos Transact APIs in the sense that the Spring Boot bank-demo application
 * routes Temenos-style requests through this SDK without rewriting payloads.
 *
 * <p>Thread-safety: instances of this class are safe for concurrent use by multiple
 * threads. Each query call creates an isolated {@link HttpRequest}.
 */
public final class GroundworkClient {

  private final GroundworkConfig config;
  private final HttpClient httpClient;
  private final ObjectMapper mapper;

  /** Construct a client with the supplied configuration and a sensible default HTTP client. */
  public GroundworkClient(GroundworkConfig config) {
    this(config, HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5))
            .version(HttpClient.Version.HTTP_1_1)
            .build());
  }

  /** Construct a client with the supplied configuration and a caller-supplied HTTP client. */
  public GroundworkClient(GroundworkConfig config, HttpClient httpClient) {
    this.config = Objects.requireNonNull(config, "config");
    this.httpClient = Objects.requireNonNull(httpClient, "httpClient");
    this.mapper = new ObjectMapper()
            .registerModule(new JavaTimeModule())
            .setPropertyNamingStrategy(PropertyNamingStrategies.SNAKE_CASE)
            .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
  }

  /**
   * Execute a query against the Groundwork runtime on behalf of an end user.
   *
   * @param question        natural-language query, sent in the request body
   * @param userAssertion   signed JWT identifying the end user (verified by Groundwork)
   * @return parsed {@link QueryResponse} containing citations (only chunks the user is
   *         permitted to see) and the immutable {@link Trace}.
   * @throws GroundworkException if the runtime returns a non-2xx status or the response
   *                             cannot be parsed.
   */
  public QueryResponse query(String question, String userAssertion) {
    return query(QueryRequest.of(question), userAssertion);
  }

  /** Execute a fully-formed query with a verified user assertion. */
  public QueryResponse query(QueryRequest request, String userAssertion) {
    Objects.requireNonNull(request, "request");
    Objects.requireNonNull(userAssertion, "userAssertion");

    URI uri = URI.create(stripTrailingSlash(config.runtimeBaseUrl()) + "/v1/query");
    byte[] body;
    try {
      body = mapper.writeValueAsBytes(request);
    } catch (IOException e) {
      throw new GroundworkException("failed to serialize request", e);
    }

    HttpRequest httpRequest = HttpRequest.newBuilder(uri)
            .timeout(config.requestTimeout())
            .header("Content-Type", "application/json")
            .header("X-Groundwork-API-Key", config.apiKey())
            .header("X-Groundwork-User-Assertion", userAssertion)
            .POST(HttpRequest.BodyPublishers.ofByteArray(body))
            .build();

    HttpResponse<byte[]> response;
    try {
      response = httpClient.send(httpRequest, HttpResponse.BodyHandlers.ofByteArray());
    } catch (IOException | InterruptedException e) {
      if (e instanceof InterruptedException) {
        Thread.currentThread().interrupt();
      }
      throw new GroundworkException("transport failure calling Groundwork", e);
    }

    int status = response.statusCode();
    if (status == 401 || status == 403) {
      throw new GroundworkException.AuthException(status, parseErrorMessage(response.body()));
    }
    if (status == 429) {
      throw new GroundworkException.RateLimitException(parseErrorMessage(response.body()));
    }
    if (status >= 400) {
      throw new GroundworkException("Groundwork returned status " + status + ": "
              + parseErrorMessage(response.body()));
    }

    try {
      return mapper.readValue(response.body(), QueryResponse.class);
    } catch (IOException e) {
      throw new GroundworkException("failed to parse Groundwork response", e);
    }
  }

  private String parseErrorMessage(byte[] body) {
    if (body == null || body.length == 0) {
      return "(empty body)";
    }
    try {
      Map<?, ?> parsed = mapper.readValue(body, Map.class);
      Object err = parsed.get("error");
      if (err != null) {
        return err.toString();
      }
    } catch (IOException ignored) {
      // fall through
    }
    return new String(body);
  }

  private static String stripTrailingSlash(String url) {
    if (url.endsWith("/")) {
      return url.substring(0, url.length() - 1);
    }
    return url;
  }
}
