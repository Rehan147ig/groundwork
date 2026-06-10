package com.groundwork.bankdemo.api;

import com.groundwork.bankdemo.persona.PersonaRegistry;
import com.groundwork.sdk.GroundworkClient;
import com.groundwork.sdk.PersonaTokenMinter;
import com.groundwork.sdk.QueryResponse;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

import java.util.Map;

/**
 * Temenos Transact-style loan / holdings endpoint. Wire-compatible with the public
 * Temenos Holdings API surface; internally routes through Groundwork.
 *
 * <p>Endpoint: {@code GET /holdings/loans/search?q=...}
 */
@RestController
@RequestMapping("/holdings/loans")
public class TransactLoanController {

  private final GroundworkClient groundwork;
  private final PersonaTokenMinter minter;
  private final PersonaRegistry personas;

  public TransactLoanController(GroundworkClient groundwork, PersonaTokenMinter minter, PersonaRegistry personas) {
    this.groundwork = groundwork;
    this.minter = minter;
    this.personas = personas;
  }

  @GetMapping(value = "/search", produces = MediaType.APPLICATION_JSON_VALUE)
  public ResponseEntity<Map<String, Object>> searchLoans(
      @RequestHeader("X-Persona") String personaId,
      @RequestParam("q") String query) {

    PersonaRegistry.Persona persona = personas.require(personaId);
    String jwt = minter.mint(persona.id());
    QueryResponse response = groundwork.query(query, jwt);

    return ResponseEntity.ok(Map.of(
        "header", Map.of(
            "transactionStatus", "Live",
            "groundworkTraceId", response.trace().traceId(),
            "groundworkDigest", response.trace().immutableDigest(),
            "decisionMode", response.trace().decisionMode(),
            "status", "success"),
        "body", Map.of(
            "query", query,
            "persona", Map.of("id", persona.id(), "displayName", persona.displayName(), "role", persona.role()),
            "loanRecords", response.citations(),
            "blockedByAcl", response.trace().blockedByAcl())));
  }
}
