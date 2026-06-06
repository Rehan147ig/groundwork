package com.groundwork.sdk;

/** Base exception for Groundwork SDK errors. */
public class GroundworkException extends RuntimeException {

  public GroundworkException(String message) { super(message); }
  public GroundworkException(String message, Throwable cause) { super(message, cause); }

  /** Authentication/authorization failure from the runtime. HTTP 401 / 403. */
  public static final class AuthException extends GroundworkException {
    private final int status;

    public AuthException(int status, String message) {
      super("Groundwork auth failure (HTTP " + status + "): " + message);
      this.status = status;
    }

    public int status() { return status; }
  }

  /** Rate limit exceeded. HTTP 429. The caller should back off. */
  public static final class RateLimitException extends GroundworkException {
    public RateLimitException(String message) {
      super("Groundwork rate limit exceeded: " + message);
    }
  }
}
