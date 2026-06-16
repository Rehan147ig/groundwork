# Deployment Procedures

## Rollout Strategy
All tier-1 services must use canary releases:
1. Deploy to 5% of traffic
2. Bake for 15 minutes, monitor error rates
3. Expand to 25% of traffic
4. Bake for 15 minutes
5. Expand to 100%

## Rollback
Automated rollback triggers if HTTP 5xx rate > 1% over a 5-minute window.
