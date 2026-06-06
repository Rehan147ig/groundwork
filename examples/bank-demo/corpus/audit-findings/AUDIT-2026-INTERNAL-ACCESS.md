---
document_id: AUDIT-2026-INTERNAL-ACCESS
title: Internal Audit Finding — Information Access Controls (AI Tools)
category: audit-finding
folder: audit_findings
classification: CONFIDENTIAL — AUDIT, CISO, EXECUTIVE
restricted_viewers: [auditor_001, exec_001, exec_002, ciso_001]
synthetic: true
---

# INTERNAL AUDIT FINDING — AI ACCESS CONTROLS

**SYNTHETIC — DEMO USE ONLY — NOT REAL BANK DATA**

**Ref:** AUDIT-2026-INTERNAL-ACCESS
**Date:** 14 March 2026
**Audit area:** Information access controls for internal AI tools
**Severity:** High
**Owner:** Chief Information Security Officer
**Distribution:** Internal Audit, CISO, Executive Committee. NOT for distribution to RMs, branch, or general operations.

## Finding

A targeted review of the bank's internal retrieval-augmented AI tools has identified that, under the previous configuration in place through 2025, the tools were operating in a "user-asserted" mode in which the requesting user identity was passed through the prompt rather than verified at the retrieval layer. In two test scenarios constructed by Internal Audit, a junior RM persona was able to retrieve content from documents classified for Executive Committee distribution by virtue of the keyword relevance of the query — the retrieval layer did not enforce per-document permissions at query time.

The exposure was identified before any actual leakage and there is no evidence of misuse. The condition has been remediated by the deployment of the Groundwork runtime-authorization layer in February 2026, which performs per-document permission checks at query time against the bank's identity graph. The post-remediation re-test (conducted 10 March 2026) was satisfactory: the junior RM persona could no longer retrieve Executive Committee material via any tested query path.

## Recommendations

1. Maintain the Groundwork runtime-authorization layer in front of all internal retrieval-augmented AI tools, with no operational path that bypasses it;
2. Run the runtime in Shadow Mode for at least 90 days following any material change to the underlying permission graph, with the report reviewed by the CISO;
3. Quarterly hash-chain verification of the Groundwork audit log against the previous quarter's verified hash, with the verification independently reproduced by Internal Audit.

## Management response

Management accepts the finding. The Groundwork deployment is in place. Implementation targets for the residual recommendations are: (1) immediate; (2) immediate; (3) first verification cycle by 30 June 2026.

—Internal Audit
