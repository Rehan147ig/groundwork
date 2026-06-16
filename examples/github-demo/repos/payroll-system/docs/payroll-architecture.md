# Payroll Architecture

## Microservices
- **PayrollEngine**: Core calculation engine
- **TaxCalculator**: Integration with tax rate provider
- **DirectDeposit**: ACH generation
- **ReportGenerator**: Paystub generation

## Infrastructure
- Backend: PostgreSQL
- Cache: Redis
- Deployment: AWS ECS
