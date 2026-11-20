package budget

import (
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// PayDate is when the salary for nominal month m arrives, given payday (1–31; 31
// means the last day): that day, clamped to the month's length, moved back to the
// previous working day if it falls on a weekend or bank holiday. The result can lie
// in the month before (January's salary on payday 1 arrives on 30 December).
func PayDate(m month.Month, payday int) Date {
	d := Date{m.Year(), m.Month(), min(payday, daysIn(m))}
	for !IsWorkingDay(d) {
		d = d.AddDays(-1)
	}
	return d
}

// Period is one pay period: from a pay date (inclusive) to the next (exclusive).
type Period struct {
	Start, Next Date
	// BudgetMonth is the nominal month of the salary that started the period; its
	// settlement says how much of the period's money is free to spend.
	BudgetMonth month.Month
}

// PeriodAt returns the pay period that today falls in.
func PeriodAt(today Date, payday int) Period {
	m := today.MonthOf()
	// Next month's salary can arrive this month (payday 1 moved back over New Year),
	// so try the latest nominal month first.
	for _, nominal := range []month.Month{m.Add(1), m, m.Add(-1)} {
		if start := PayDate(nominal, payday); !start.After(today) {
			return Period{Start: start, Next: PayDate(nominal.Add(1), payday), BudgetMonth: nominal}
		}
	}
	nominal := m.Add(-2) // unreachable for paydays 1–31, kept for safety
	return Period{Start: PayDate(nominal, payday), Next: PayDate(nominal.Add(1), payday), BudgetMonth: nominal}
}

// Days is the length of the period in days.
func (p Period) Days() int { return p.Start.DaysUntil(p.Next) }

// DaysLeft counts the days from today (included) until the next pay date.
func (p Period) DaysLeft(today Date) int { return today.DaysUntil(p.Next) }

// ShouldHave is how much of the period's free money should be left today if it is
// spent evenly: leftover × days left / days in the period, rounded half-even.
func ShouldHave(leftover money.Ore, p Period, today Date) money.Ore {
	return money.DivRound(leftover*money.Ore(p.DaysLeft(today)), int64(p.Days()))
}
