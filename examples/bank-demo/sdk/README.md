# Groundwork Java SDK

Thin Java client for the Groundwork runtime authorization layer. Builds on JDK 17, depends on Jackson and JJWT.

## Maven

```xml
<dependency>
  <groupId>com.groundwork</groupId>
  <artifactId>groundwork-sdk</artifactId>
  <version>0.1.0</version>
</dependency>
```

## Use

```java
import com.groundwork.sdk.*;

GroundworkClient client = new GroundworkClient(
    GroundworkConfig.builder()
        .runtimeBaseUrl("https://groundwork.bank.internal:8080")
        .apiKey(System.getenv("GROUNDWORK_API_KEY"))
        .build());

// In production: pass a JWT minted by your IdP (Entra, Okta, Auth0, ...).
String userJwt = yourIdp.mintJwtForCurrentUser();

QueryResponse response = client.query("quarterly portfolio review", userJwt);

for (Citation c : response.citations()) {
  System.out.printf("doc %s chunk %s score %.3f%n",
      c.documentId(), c.chunkId(), c.score());
}

Trace trace = response.trace();
System.out.printf("trace %s digest %s decision_mode %s latency %dms blocked %d%n",
    trace.traceId(), trace.immutableDigest(), trace.decisionMode(),
    trace.latencyMs(), trace.blockedByAcl());
```

## Demo-only helper: `PersonaTokenMinter`

The SDK ships with `PersonaTokenMinter`, an HS256 JWT signer, **for demos only**. Production callers should not use it — instead, pass JWTs minted by the customer's identity provider.

## Headers the SDK sets

| Header | Purpose |
| --- | --- |
| `X-Groundwork-API-Key` | tenant identifier |
| `X-Groundwork-User-Assertion` | verified end-user JWT (`sub` claim is the user id) |
| `Content-Type` | `application/json` |

The runtime never trusts `tenant_id`, `region`, or `user_id` from the request body. The SDK does not send them.

## Building

```bash
mvn package
# produces target/groundwork-sdk-0.1.0.jar + target/groundwork-sdk-0.1.0-sources.jar
```

To install into your local Maven repo so other projects in the monorepo can depend on it:

```bash
mvn install
```

## License

Apache 2.0 (matches the rest of Groundwork).
