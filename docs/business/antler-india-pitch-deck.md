# Groundwork: Antler India Pitch Deck Master Document

This document consolidates every piece of strategy, architecture, and roadmap for Groundwork into a single narrative flow designed specifically for the Antler India Investment Committee.

---

## 1. The Hook: The Crisis of AI Oversharing

**The Scenario:**
A Fortune 500 bank deploys an internal AI assistant. A marketing employee types: *"Summarize the board's discussion on executive compensation from last quarter."* 
The AI bypasses human permissions, reads the confidential board minutes, and returns the CEO's salary and layoff plans to the employee. 

**The Market Reality:**
- 68% of CISOs say AI data leakage is their #1 concern.
- Enterprises are **blocking AI deployments entirely** because they cannot control what the AI reads.
- **The Problem:** AI models understand semantics, not permissions. If a document matches the question, the AI returns it, regardless of who is asking. 
- **Current Solutions (Tagging):** Ingestion-time tagging is broken. If an employee is fired at 2:00 PM, their access tags are still cached at 2:01 PM.

---

## 2. The Solution: Groundwork

**What is Groundwork?**
Groundwork is a **Runtime Authorization Infrastructure** for AI applications. It sits between the AI Agent and the Enterprise Knowledge Base. 

It is not a chatbot. It is not another RAG platform. It is the security proxy that makes enterprise AI possible.

**How it works (The 98-millisecond loop):**
1. **Identify:** Authenticates the AI request via tenant-scoped, bcrypt-hashed API keys.
2. **Retrieve:** Fetches top 50 semantic candidates from the vector database.
3. **Enforce:** Checks the human's *live* permissions against every single chunk using OpenFGA (Google Zanzibar ReBAC).
4. **Strip:** Rips out any unauthorized data.
5. **Audit:** Writes an immutable, cryptographically hash-chained log to PostgreSQL.
6. **Return:** Only the clean, permitted data goes to the AI.

---

## 3. The Business Model: Liability Transfer

**Scapegoat-as-a-Service**
When a Fortune 500 company buys Groundwork, they aren't just buying software. They are buying a **legal shield**. 
If a CISO's internal team builds AI security and it fails, the CISO is fired. If they buy Groundwork and a breach occurs, they blame the vendor, and their Cyber Liability Insurance covers the damages. 

**The Build vs. Buy Math:**
- Internal Build: 3 Senior Engineers ($600k/yr) + 9 months to production.
- Buy Groundwork: $60k/yr Enterprise License + 1-week integration.

---

## 4. The Architecture & The "Fail-Closed" Guarantee

Groundwork is engineered around one non-negotiable principle: **Fail-Closed Security.**

> *Under no circumstances—crash, timeout, network failure, or database overload—will Groundwork accidentally return unauthorized data.*

**The Resilience Engineering:**
- **Circuit Breakers:** If the authorization engine (OpenFGA) goes down, Groundwork instantly trips the circuit, blocking all data rather than hanging the network.
- **Explicit Error Routing:** Every failure (`acl_timeout`, `acl_backend_unavailable`, `acl_denied`) explicitly routes to a "Strip Chunk" command. Zero data leakage.
- **Concurrency Semaphore:** Evaluates 50 permission checks in parallel in under 15ms without DDoSing the policy engine.

---

## 5. The Live Demo (The "Aha" Moment)

**The Setup:**
Groundwork runs natively as a Tool for AI agents using the **Model Context Protocol (MCP)**. 

**The Proof:**
1. **The Hacker Test:** We simulate an unauthorized user asking: *"How does live ACL fail closed?"*
   - *Result:* Groundwork intercepts, finds the policy, evaluates permissions, and **blocks it**. The AI receives 0 documents. Trace ID logged.
2. **The Authorized Test:** A `finance_user` asks the exact same question.
   - *Result:* Groundwork authenticates the identity, passes the document through securely. The AI answers the question. 

*Conclusion:* The AI is completely subordinate to human identity in real-time.

---

## 6. The Seed Roadmap (What Antler's Capital Buys)

The core engine is built, tested, and hardened. Antler's capital will fund the Enterprise Go-To-Market features (Target: 10 weeks).

### 1. Shadow Mode (The Adoption Unlocker)
- **The Problem:** Enterprises are terrified of breaking existing apps with active blocking.
- **The Feature:** Groundwork runs silently, evaluating permissions and logging violations into a dashboard *without blocking the data*. Once the CISO sees the dashboard of active leaks, they flip the switch to "Enforce Mode."

### 2. Enterprise Identity Sync (Okta / Entra ID)
- Native webhooks to synchronize Microsoft Active Directory and Okta groups directly into Groundwork's OpenFGA engine. No manual user management.

### 3. Java SDK & Spring Boot Integration
- Banks and Fortune 500s run on Java. We will provide a native Spring Boot starter with OIDC support, published to Maven Central, alongside Python/TypeScript SDKs.

### 4. Oracle Cloud & Air-Gapped Deployments
- Containerized Kubernetes/Helm charts allowing banks to deploy Groundwork inside their own locked-down VPC (Bring Your Own Cloud) to respect data gravity.

---

## 7. The Founder Advantage (Why Us?)

**The Speed of Execution:**
We built a production-grade Go engine, a cryptographic audit chain, a full MCP gateway, and a resilient security architecture in **days, not months.** 

By leveraging advanced AI engineering models (Codex/Claude), we operate at 10x the speed of a traditional engineering team. We don't need Antler's money to hire developers to write code. We need the capital for **SOC2 certification, legal liability frameworks, and enterprise sales/marketing** to get in front of the CISOs who are desperate for this solution right now.
