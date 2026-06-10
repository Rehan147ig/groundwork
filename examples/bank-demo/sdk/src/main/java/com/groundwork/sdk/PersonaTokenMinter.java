package com.groundwork.sdk;

import io.jsonwebtoken.Jwts;
import io.jsonwebtoken.security.Keys;

import javax.crypto.SecretKey;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.Date;
import java.util.Objects;

/**
 * Mints HS256 JWTs for the demo, in the same shape the Groundwork runtime accepts via
 * the {@code X-Groundwork-User-Assertion} header.
 *
 * <p><strong>DEMO USE ONLY.</strong> A production deployment passes a JWT minted by the
 * customer's identity provider (Entra ID, Okta, Auth0, etc.). This class exists so the
 * demo Spring Boot bank-tool can show per-persona behavior without standing up a full
 * IdP. The shared secret used here corresponds to the {@code GROUNDWORK_JWT_HS_SECRET}
 * environment variable on the runtime.
 */
public final class PersonaTokenMinter {

  private final SecretKey key;
  private final Duration ttl;

  /**
   * @param hs256Secret the shared HS256 secret, in clear text. Must match the runtime's
   *                    {@code GROUNDWORK_JWT_HS_SECRET}.
   * @param ttl         token lifetime.
   */
  public PersonaTokenMinter(String hs256Secret, Duration ttl) {
    Objects.requireNonNull(hs256Secret, "hs256Secret");
    this.key = Keys.hmacShaKeyFor(hs256Secret.getBytes(StandardCharsets.UTF_8));
    this.ttl = ttl != null ? ttl : Duration.ofHours(1);
  }

  /** Mint a JWT for the given subject (the persona id). */
  public String mint(String subject) {
    Instant now = Instant.now();
    return Jwts.builder()
            .subject(subject)
            .issuedAt(Date.from(now))
            .expiration(Date.from(now.plus(ttl)))
            .signWith(key)
            .compact();
  }
}
