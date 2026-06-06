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
 * Temenos Transact-style customer endpoint. Wire-compatible with the public Temenos
 * Customer Management API surface; internally routes through Groundwork so every
 * document fetch is permission-gated against the bank's identity graph.
 *
 * <p>Endpoint: {@code GET /party/customers/{customerId}/documents?q=...}
 *
 * <p>What this demonstrates: a bank developer who already knows the Temenos REST surface
 * gets a familiar API shape, but the access enforcement happens in Groundwork rather
 * than relying on the bank's existing role-based access in Temenos. The Groundwork
 * layer can express far finer-grained permissions (per-document, per-folder, per-tag)
 * than the underlying core's RBAC.
 */
@RestController
@RequestMapping("/party/customers")
public class TransactCustomerController {

  private final GroundworkClient groundwork;
  private final PersonaTokenMinter minter;
  private final PersonaRegistry personas;

  public TransactCustomerController(GroundworkClient groundwork, PersonaTokenMinter minter, PersonaRegistry personas) {
    this.groundwork = groundwork;
    this.minter = minter;
    this.personas = personas;
  }

  @GetMapping(value = "/{customerId}/documents", produces = MediaType.APPLICATION_JSON_VALUE)
  public ResponseEntity<Map<String, Object>> customerDocuments(
      @RequestHeader("X-Persona") String personaId,
      @PathVariable String customerId,
      @RequestParam(name = "q", defaultValue = "") String query) {

    PersonaRegistry.Persona persona = personas.require(personaId);
    String question = query.isBlank()
            ? "Documents for customer " + customerId
            : (query + " for customer " + customerId);

    String jwt = minter.mint(persona.id());
    QueryResponse response = groundwork.query(question, jwt);

    // Temenos-style envelope: header + body, status, audit.
    Map<String, Object> header = Map.of(
        "transactionStatus", "Live",
        "audit", Map.of(
            "groundworkTraceId", response.trace().traceId(),
            "groundworkDigest", response.trace().immutableDigest(),
            "decisionMode", response.trace().decisionMode(),
            "latencyMs", response.trace().latencyMs()),
        "id", customerId,
        "status", "success");

    Map<String, Object> body = Map.of(
        "customerId", customerId,
        "persona", Map.of("id", persona.id(), "displayName", persona.displayName(), "role", persona.role()),
        "documents", response.citations(),
        "blockedByAcl", response.trace().blockedByAcl(),
        "accessDecisions", response.trace().accessDecisions());

    return ResponseEntity.ok(Map.of("header", header, "body", body));
  }
}
