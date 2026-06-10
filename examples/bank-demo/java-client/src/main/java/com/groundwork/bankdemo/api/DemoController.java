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
 * Generic demo endpoint. Accepts a persona via the {@code X-Persona} header and a
 * question in the body, mints the persona JWT, and calls Groundwork.
 *
 * <p>Endpoint: {@code POST /demo/query}
 * <pre>
 *   curl -X POST http://localhost:9090/demo/query \
 *     -H 'X-Persona: rm_tony' \
 *     -H 'Content-Type: application/json' \
 *     -d '{"question": "Stark Industries credit memo"}'
 * </pre>
 *
 * <p>The persona's display name and role are echoed back so the demo UI / output makes
 * the persona substitution legible to a viewer.
 */
@RestController
@RequestMapping("/demo")
public class DemoController {

  private final GroundworkClient groundwork;
  private final PersonaTokenMinter minter;
  private final PersonaRegistry personas;

  public DemoController(GroundworkClient groundwork, PersonaTokenMinter minter, PersonaRegistry personas) {
    this.groundwork = groundwork;
    this.minter = minter;
    this.personas = personas;
  }

  @PostMapping(value = "/query", produces = MediaType.APPLICATION_JSON_VALUE)
  public ResponseEntity<Map<String, Object>> query(
      @RequestHeader("X-Persona") String personaId,
      @RequestBody Map<String, Object> body) {

    PersonaRegistry.Persona persona = personas.require(personaId);
    String question = (String) body.getOrDefault("question", "");

    String jwt = minter.mint(persona.id());
    QueryResponse response = groundwork.query(question, jwt);

    return ResponseEntity.ok(Map.of(
        "persona", Map.of("id", persona.id(), "display_name", persona.displayName(), "role", persona.role()),
        "question", question,
        "citations", response.citations(),
        "trace", response.trace()));
  }

  @GetMapping(value = "/personas", produces = MediaType.APPLICATION_JSON_VALUE)
  public Map<String, PersonaRegistry.Persona> listPersonas() {
    return personas.all();
  }
}
