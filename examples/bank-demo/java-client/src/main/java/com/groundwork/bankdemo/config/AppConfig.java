package com.groundwork.bankdemo.config;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.groundwork.bankdemo.persona.PersonaRegistry;
import com.groundwork.sdk.GroundworkClient;
import com.groundwork.sdk.GroundworkConfig;
import com.groundwork.sdk.PersonaTokenMinter;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import java.nio.file.Files;
import java.nio.file.Paths;
import java.time.Duration;

/**
 * Spring configuration. Wires the Groundwork SDK client, the per-demo persona registry,
 * and the JWT minter from environment variables and the {@code personas.json} file.
 *
 * <p>Configuration sources (in priority order):
 * <ol>
 *   <li>Environment variables ({@code GROUNDWORK_RUNTIME_URL}, {@code GROUNDWORK_API_KEY},
 *       {@code GROUNDWORK_JWT_HS_SECRET}, {@code BANK_DEMO_PERSONAS_FILE})</li>
 *   <li>{@code application.yml} defaults</li>
 * </ol>
 */
@Configuration
public class AppConfig {

  @Value("${groundwork.runtime-url:http://localhost:8080}")
  private String runtimeUrl;

  @Value("${groundwork.api-key:gw_local_demo_bank_key}")
  private String apiKey;

  @Value("${groundwork.jwt-secret:demo-only-replace-in-production}")
  private String jwtSecret;

  @Value("${bank-demo.personas-file:./personas/personas.json}")
  private String personasFile;

  @Bean
  public GroundworkClient groundworkClient() {
    GroundworkConfig cfg = GroundworkConfig.builder()
            .runtimeBaseUrl(runtimeUrl)
            .apiKey(apiKey)
            .requestTimeout(Duration.ofSeconds(10))
            .build();
    return new GroundworkClient(cfg);
  }

  @Bean
  public PersonaTokenMinter personaTokenMinter() {
    return new PersonaTokenMinter(jwtSecret, Duration.ofHours(1));
  }

  @Bean
  public PersonaRegistry personaRegistry(ObjectMapper mapper) throws Exception {
    byte[] data = Files.readAllBytes(Paths.get(personasFile));
    return PersonaRegistry.fromJson(mapper, data);
  }
}
