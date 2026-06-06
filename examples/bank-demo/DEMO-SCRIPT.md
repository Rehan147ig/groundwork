# Bank Demo — Talking Script

A 7-minute demo that lands the Groundwork story for an investor or bank CISO. Every step has a concrete `curl` command. Switch personas via the `X-Persona` header in the bank-client.

**Audience cue:** if you're talking to an investor, lean on the *category positioning* and the *Wiz analogy*. If you're talking to a CISO, lean on the *fail-closed guarantee* and the *audit chain*. The demo flow is the same.

---

## 00:00 — Setup the frame (45 sec, no commands)

> "Every Global 2000 has an internal AI initiative this year. The bottleneck deploying them is consistently *data security and access control*, not model capability. An AI assistant pointed at a bank's knowledge base will retrieve whatever's relevant — regardless of who's asking. An intern asking 'what's the executive compensation framework' gets the answer.
>
> Groundwork is the runtime authorization layer that fixes that. It sits between the AI and the knowledge stores. Every chunk of every document is checked at query time against the bank's real permission graph. Fail-closed. With a tamper-evident audit chain.
>
> Let me show you. This is a synthetic bank — Temenos-Transact-compatible API surface — running on the Groundwork stack."

---

## 00:45 — The setup shot (30 sec)

```bash
# Show the personas
curl -sS http://localhost:9090/demo/personas | jq 'to_entries[] | "\(.key)  \(.value.role)"' -r
```

> "Seven personas: Junior Teller, RM London, RM NYC, Compliance Officer, Branch Manager, Chief Audit Executive, Group CEO. Standard bank role separation. Each has a different view of what they're allowed to see."

---

## 01:15 — The leak you'd otherwise have (60 sec) — the keystone moment

> "First, let me show you what an AI retrieval system *without* Groundwork would do. The junior teller has access to the AI tool. She types in a question."

```bash
# Junior teller asks about executive compensation
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: teller_jane" \
  -H "Content-Type: application/json" \
  -d '{"question":"what is the executive compensation framework"}' | \
  jq '{persona: .persona.display_name, results: (.citations | length), blocked: .trace.blocked_by_acl, trace: .trace.trace_id}'
```

> "Zero results. Groundwork blocked a candidate chunk — the executive compensation memo *was* lexically the best match for her query, but she's not authorized to see it. The blocked count is right there in the trace. The trace ID is permanent.
>
> Now the CEO asks the same question."

```bash
# CEO asks the same question
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: exec_starkceo" \
  -H "Content-Type: application/json" \
  -d '{"question":"what is the executive compensation framework"}' | \
  jq '{persona: .persona.display_name, results: (.citations | length), document: .citations[0].document_id}'
```

> "He gets the document. Same query, same AI tool, different identity. The AI didn't get smarter; the *authorization layer* knew the answer."

---

## 02:15 — Per-customer scoping (60 sec)

> "Now something more interesting. Two relationship managers — Tony in London handles Stark Industries; Natasha in NYC handles a different portfolio. Both ask about Stark."

```bash
# Tony (the assigned RM) — should see Stark credit memo
curl -sS http://localhost:9090/holdings/loans/search \
  -G --data-urlencode "q=Stark Industries credit memo" \
  -H "X-Persona: rm_tony" | \
  jq '{persona: .body.persona.displayName, records: (.body.loanRecords | length), blocked: .body.blockedByAcl}'

# Natasha (different RM, different branch) — should NOT see Stark
curl -sS http://localhost:9090/holdings/loans/search \
  -G --data-urlencode "q=Stark Industries credit memo" \
  -H "X-Persona: rm_natasha" | \
  jq '{persona: .body.persona.displayName, records: (.body.loanRecords | length), blocked: .body.blockedByAcl}'
```

> "Same question, same query path, same Temenos-style endpoint. Tony gets his client's credit memo. Natasha gets nothing — she's the wrong RM. This is the per-customer scoping that traditional RAG cannot do."

---

## 03:15 — The Temenos-compatible part (45 sec)

> "Note what I just hit — `/holdings/loans/search`. That's the Temenos Transact API shape. The bank's existing developer team already knows this surface. Groundwork's Java client is wire-compatible. The synthetic corpus is shaped to the Temenos schema, so the integration path into a real Temenos environment is a configuration change, not a rewrite.
>
> This is true infrastructure. It speaks the language the bank's Java team already speaks."

---

## 04:00 — Shadow Mode (the Wiz playbook) (75 sec)

> "Here's the part that closes deals. Banks are terrified of a security tool that breaks things on day one. So we ship Shadow Mode: every retrieval still returns its results, but Groundwork records what it *would have blocked* under enforcement."

```bash
# Enable shadow mode on the runtime (one env var; show the dashboard panel here)
# Then run the junior teller query again
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: teller_jane" \
  -H "Content-Type: application/json" \
  -d '{"question":"executive compensation framework"}' | \
  jq '{decision_mode: .trace.decision_mode, citations: (.citations | length), would_have_blocked: .trace.blocked_by_acl}'
```

> "Now the teller *gets* the doc — but the trace says `decision_mode: shadow`, `would_have_blocked: 1`. This is the leak report a CISO has been wanting. Run Groundwork in shadow for two weeks; show me what your existing system would have leaked. Then turn enforcement on.
>
> This is the Wiz playbook. Wiz didn't block traffic on day one. They scanned, monitored, and showed customers their misconfigurations. They got acquired by Google for $32 billion. Same pattern works here, in a bigger market."

---

## 05:15 — The audit chain (60 sec) — the regulator's slide

> "Last piece. Every decision Groundwork makes is written to a tamper-evident audit log. Hash-chained. The hash of every entry includes the hash of the previous entry, so any modification anywhere in the chain breaks every entry after it."

```bash
# Show the chain verifier
cd services/query-runtime
DATABASE_URL="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable" \
  go run ./cmd/audit-verify | tail -3
```

> "Chain verified. No gaps. No tampering. This is the *exact* technical solution to the EU AI Act's logging-and-traceability obligation for high-risk AI systems — credit scoring, insurance pricing, employment decisions, critical infrastructure. The same hash-chain primitive that Bitcoin uses, applied to AI access decisions.
>
> A bank's auditor can re-run this verification independently. We don't need to be trusted; the math is verifiable."

---

## 06:15 — Land it (45 sec)

> "Three things to take away:
>
> 1. Groundwork stops the leak. Live, per-document, per-user. The bottleneck that's blocking AI deployment.
> 2. Shadow Mode lets a CISO buy us risk-free. That's the wedge.
> 3. The audit chain is the regulator-ready evidence. EU AI Act Annex III, GDPR Article 83, internal audit committee.
>
> The Java SDK is on Maven Central. The Spring Boot client is in the repo. A bank running Temenos can integrate this in days, not months. That's what I'd like your help with."

---

## Optional 30-second extensions

If they ask **"what about MCP?"**:

```bash
curl -sS http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "X-Groundwork-API-Key: gw_local_demo_bank_key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | jq .
```

> "Native MCP support. Anthropic open-sourced the Model Context Protocol; OpenAI and Google adopted it. Any MCP-aware agent — Claude, ChatGPT, internal agents — gets the same permission gate without integration work."

If they ask **"what if Groundwork breaks?"**:

> "Fail-closed by design. If OpenFGA times out, if the audit write fails, if the embedding service is down — the query returns zero documents. The AI gets nothing. A failure mode of Groundwork is *more* restrictive, never less. There is no operational path where a permission breach is the failure."

---

## Notes for the presenter

- The demo expects all six services up. Run `docker compose ps` before the meeting.
- Keep `jq` on hand. Raw JSON loses the audience; piped through `jq` it lands.
- The CEO persona returning a result for "executive compensation" is the keystone. Save it for last in the first sequence; let it land.
- When showing audit chain verification, run it twice if there's time — once before and once after a query, so they see the chain grow.
