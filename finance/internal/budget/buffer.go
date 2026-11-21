package budget

import (
	"errors"
	"sort"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
)

// ErrNoPayday means someone who transfers to the felleskonto has no payday, so
// their transfers can't be dated.
var ErrNoPayday = errors.New("budget: a payday is missing")

// Buffer is how much the felleskonto must hold, worked out from the dates money
// actually moves.
type Buffer struct {
	// Required is the least the account must hold at `from` so that it never goes
	// below zero over the next twelve months.
	Required money.Ore
	// LowDate is when it would be lowest, and LowBill the bill drawn that day
	// (the biggest irregular one if any). Zero when Required is 0.
	LowDate Date
	LowBill string
}

type flow struct {
	date      Date
	amount    money.Ore // positive in, negative out
	bill      string
	irregular bool
}

// FelleskontoBuffer simulates the felleskonto from `from` (inclusive) for twelve
// months. Each person's monthly transfer (the smoothed amount from the settlement)
// arrives on their pay date for that month; each bill paid from the felleskonto is
// drawn on its due day (not set: the 1st; past the month's end: its last day). On a
// day with both, bills go first. Bills on weekends are counted on the stated day,
// which errs on the cautious side. Money that arrived before `from` is part of the
// balance being compared with Required.
func FelleskontoBuffer(in ledger.Input, from Date) (Buffer, error) {
	end := DateOf(from.utc().AddDate(1, 0, 0))
	var flows []flow
	// A month's salary can arrive in the month before (payday 1 over New Year), so
	// start one month early; the window filter below drops what's out of range.
	for nm := from.MonthOf().Add(-1); nm <= end.MonthOf(); nm++ {
		st, err := ledger.Settle(in, nm)
		if err != nil {
			return Buffer{}, err
		}
		for _, u := range st.Users {
			if u.ToFelleskonto == 0 {
				continue
			}
			inc, ok := ledger.IncomeAt(in.Incomes[u.User], nm)
			if !ok || inc.Payday == 0 {
				return Buffer{}, ErrNoPayday
			}
			flows = append(flows, flow{date: PayDate(nm, inc.Payday), amount: u.ToFelleskonto})
		}
		for _, d := range st.Due {
			if d.Kind != ledger.Shared || d.Payer != ledger.Felleskonto {
				continue
			}
			day := max(d.DueDay, 1)
			flows = append(flows, flow{
				date:   Date{nm.Year(), nm.Month(), min(day, daysIn(nm))},
				amount: -d.Amount, bill: d.Name, irregular: d.PerYear <= ledger.IrregularPerYear,
			})
		}
	}
	sort.SliceStable(flows, func(i, j int) bool {
		if flows[i].date != flows[j].date {
			return flows[i].date.Before(flows[j].date)
		}
		return flows[i].amount < 0 && flows[j].amount >= 0 // bills before transfers
	})

	var b Buffer
	var running, lowest money.Ore
	for _, f := range flows {
		if f.date.Before(from) || !f.date.Before(end) {
			continue
		}
		running += f.amount
		if running < lowest {
			lowest, b.LowDate = running, f.date
		}
	}
	b.Required = -lowest
	if b.Required > 0 {
		b.LowBill = billOn(flows, b.LowDate)
	}
	return b, nil
}

// billOn names the bill that explains a low point: the biggest irregular bill
// drawn that day, or else the biggest bill.
func billOn(flows []flow, d Date) string {
	var best, bestIrregular flow
	for _, f := range flows {
		if f.date != d || f.amount >= 0 {
			continue
		}
		if f.amount < best.amount {
			best = f
		}
		if f.irregular && f.amount < bestIrregular.amount {
			bestIrregular = f
		}
	}
	if bestIrregular.bill != "" {
		return bestIrregular.bill
	}
	return best.bill
}
