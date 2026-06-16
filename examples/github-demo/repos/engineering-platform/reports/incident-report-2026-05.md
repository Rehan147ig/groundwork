# Incident Report: 2026-05-15

**Outage Duration**: 23 minutes
**Impact**: Login service degraded for 15% of users.

**Root Cause**: Database connection pool exhaustion during a marketing spike.
**Mitigation**:
1. Increased PgBouncer connection pool size from 200 to 500.
2. Implemented circuit breakers in the downstream services.
