package com.groundwork.bankdemo.persona;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.NoSuchElementException;

/**
 * Loads the demo persona list from {@code personas.json} and exposes lookup by persona id.
 * Used by the bank-demo controllers to resolve a request's {@code X-Persona} header into
 * a JWT subject for {@link com.groundwork.sdk.PersonaTokenMinter}.
 *
 * <p>In a real bank deployment this class would not exist; the front-door auth flow
 * (SAML / OIDC) would already have established the user's identity and the bank tool
 * would forward the resulting JWT to Groundwork. The demo uses {@code X-Persona} as a
 * stand-in so an investor can switch personas with a single header.
 */
public final class PersonaRegistry {

  public record Persona(String id, String displayName, String role) {}

  private final Map<String, Persona> personas;

  private PersonaRegistry(Map<String, Persona> personas) {
    this.personas = personas;
  }

  public static PersonaRegistry fromJson(ObjectMapper mapper, byte[] json) throws Exception {
    JsonNode root = mapper.readTree(json);
    JsonNode arr = root.path("personas");
    Map<String, Persona> map = new LinkedHashMap<>();
    if (arr.isArray()) {
      for (JsonNode p : arr) {
        String id = p.path("id").asText();
        String displayName = p.path("display_name").asText();
        String role = p.path("role").asText();
        map.put(id, new Persona(id, displayName, role));
      }
    }
    return new PersonaRegistry(Map.copyOf(map));
  }

  public Persona require(String personaId) {
    Persona p = personas.get(personaId);
    if (p == null) {
      throw new NoSuchElementException("unknown persona '" + personaId + "'");
    }
    return p;
  }

  public Map<String, Persona> all() {
    return personas;
  }
}
