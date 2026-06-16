# Compensation Logic

Base salary is calculated proportionally to days worked in the pay period.

## Overtime Rules
- Standard OT: 1.5x base rate for hours > 40/week
- Holiday Premium: 2.0x base rate
- Shift Differential: +15% base rate for night shift (10PM - 6AM)

```go
func CalcOvertime(baseRate float64, hours float64) float64 {
    return baseRate * 1.5 * (hours - 40)
}
```