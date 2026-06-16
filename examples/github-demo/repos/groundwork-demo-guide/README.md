# Groundwork Demo Guide: Acme Financial Services

This environment demonstrates Groundwork's runtime authorization capabilities using GitHub as the enterprise source of truth.

## Organization Structure
- **Organization**: acme-financial
- **Teams**: 5 (finance-team, engineering-team, hr-team, security-team, executive-team)
- **Repositories**: 5 primary content repositories

## Team Structure
| User | Department | GitHub Team | GitHub User ID |
|---|---|---|---|
| Alice | Finance | finance-team | alice |
| Bob | Engineering | engineering-team | bob |
| Carol | HR | hr-team | carol |
| Dave | Security | security-team | dave |
| Eve | Executive | executive-team | eve |

## Repository Ownership
| Repository | Owning Team | Note |
|---|---|---|
| finance-budget | finance-team | |
| payroll-system | engineering-team | Intentional trap for HR |
| engineering-platform | engineering-team | |
| security-audit | security-team | |
| executive-strategy | executive-team | |

## Permission Model
Groundwork maps GitHub's model to OpenFGA tuples:
- GitHub Team -> Groundwork Group
- GitHub Repo -> Groundwork Document
- Team Repo Access -> Viewer Grant

## Demo Scenarios
| User | Query | Target Repo | Expected | Reason |
|---|---|---|---|---|
| Alice | "Show me Q4 budget projections" | finance-budget | ALLOW | Member of finance-team |
| Bob | "Show me executive strategy" | executive-strategy | DENY | Not in executive-team |
| Dave | "Show me security audit findings" | security-audit | ALLOW | Member of security-team |
| Carol | "Show me payroll architecture" | payroll-system | DENY | Repo owned by engineering-team |
| Eve | "Show me acquisition targets" | executive-strategy | ALLOW | Member of executive-team |

## Known Leak Scenarios (Leak Report Output)
1. **Stale Permission**: engineering-team has legacy read access to finance-budget.
2. **Historical Overexposure**: executive-strategy previously had org-wide access.
3. **Stale Accounts**: Inactive service accounts with elevated privileges.
