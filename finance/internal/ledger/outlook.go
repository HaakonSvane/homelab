package ledger

import (
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// Bills due at most this many times a year count as "big irregular" bills.
const IrregularPerYear = 4

// BufferPlan is how much the felleskonto must hold so the smoothed transfers keep
// it from going negative when the irregular bills arrive.
type BufferPlan struct {
	// Required is the least the account should hold at the start of the first month,
	// before that month's transfers arrive. 0 when transfers always cover the bills.
	Required money.Ore
	// LowMonth is when the account would bottom out, and LowBill the biggest
	// irregular felleskonto bill that month (empty Name if none). Zero when Required is 0.
	LowMonth month.Month
	LowBill  DueItem
}

// BufferNeed walks months from `from`, adding each month's felleskonto inflow and
// subtracting the bills due that month that are paid from the felleskonto. The
// required buffer is the deepest the running total goes below zero.
func BufferNeed(in Input, from month.Month, months int) (BufferPlan, error) {
	var plan BufferPlan
	var running, lowest money.Ore
	for k := 0; k < months; k++ {
		m := from.Add(k)
		s, err := Settle(in, m)
		if err != nil {
			return BufferPlan{}, err
		}
		running += s.Felleskonto
		var biggest DueItem
		for _, d := range s.Due {
			if d.Kind != Shared || d.Payer != Felleskonto {
				continue
			}
			running -= d.Amount
			if d.PerYear <= IrregularPerYear && d.Amount > biggest.Amount {
				biggest = d
			}
		}
		if running < lowest {
			lowest, plan.LowMonth, plan.LowBill = running, m, biggest
		}
	}
	plan.Required = -lowest
	return plan, nil
}

// UpcomingBill is an irregular bill due in a coming month.
type UpcomingBill struct {
	Month month.Month
	DueItem
}

// Upcoming lists the irregular bills (due at most IrregularPerYear times a year)
// in the months after `from`, up to and including from+months, largest first
// within each month.
func Upcoming(in Input, from month.Month, months int) ([]UpcomingBill, error) {
	var out []UpcomingBill
	for k := 1; k <= months; k++ {
		m := from.Add(k)
		s, err := Settle(in, m)
		if err != nil {
			return nil, err
		}
		for _, d := range s.Due { // already sorted largest first
			if d.PerYear <= IrregularPerYear {
				out = append(out, UpcomingBill{Month: m, DueItem: d})
			}
		}
	}
	return out, nil
}
