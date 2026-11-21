package budget

import (
	"errors"
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

const (
	userA ledger.UserID = 1
	userB ledger.UserID = 2
)

func incomeWithPayday(payday int) []ledger.IncomeVersion {
	return []ledger.IncomeVersion{{ValidFrom: month.MustParse("2026-01"), Amount: money.Kr(40_000), Payday: payday}}
}

func bill(id int64, name string, kr int64, due month.Mask, day int) ledger.Expense {
	return ledger.Expense{ID: id, Name: name, Kind: ledger.Shared, Versions: []ledger.ExpenseVersion{{
		ValidFrom: month.MustParse("2026-01"), Amount: money.Kr(kr), Due: due, DueDay: day,
		Shares: []ledger.Share{{User: userA, BP: 5000}, {User: userB, BP: 5000}},
	}}}
}

func household(paydayA, paydayB int, bills ...ledger.Expense) ledger.Input {
	return ledger.Input{
		Users:    []ledger.UserID{userA, userB},
		Incomes:  map[ledger.UserID][]ledger.IncomeVersion{userA: incomeWithPayday(paydayA), userB: incomeWithPayday(paydayB)},
		Expenses: bills,
	}
}

func TestFelleskontoBuffer(t *testing.T) {
	for _, c := range []struct {
		name    string
		in      ledger.Input
		from    Date
		want    money.Ore
		lowDate Date
		lowBill string
	}{
		// A 10 000 kr loan drawn on the 1st (due day not set), paid for by 5 000 kr
		// transfers on the 12th and the 26th. From 22 Nov: +5 000 on 26 Nov, then
		// −10 000 on 1 Dec → −5 000; the same pattern repeats every month. The
		// month-level model (transfers first) would say no buffer is needed.
		{"bill before the transfers", household(12, 26, bill(1, "Lån", 10_000, month.Monthly, 0)),
			Date{2026, 11, 22}, money.Kr(5_000), Date{2026, 12, 1}, "Lån"},
		// The same loan drawn on the 28th, after both transfers: never negative.
		{"bill after the transfers", household(12, 26, bill(1, "Lån", 10_000, month.Monthly, 28)),
			Date{2026, 11, 1}, 0, Date{}, ""},
		// Drawn on the 12th, the same day A's transfer lands: the bill goes first.
		{"same day: bill first", household(12, 26, bill(1, "Lån", 10_000, month.Monthly, 12)),
			Date{2026, 11, 1}, money.Kr(10_000), Date{2026, 11, 12}, "Lån"},
		// A 12 000 kr yearly bill in February on "the 31st" (so 28 February 2027),
		// smoothed to 1 000 kr a month, 500 from each on the 26th (moved to 23 Dec
		// over Christmas). Four transfers by then: 4 000 − 12 000 = −8 000.
		{"yearly bill, clamped day", household(26, 26, bill(1, "Forsikring", 12_000, month.Yearly(time.February), 31)),
			Date{2026, 11, 1}, money.Kr(8_000), Date{2027, 2, 28}, "Forsikring"},
	} {
		got, err := FelleskontoBuffer(c.in, c.from)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.Required != c.want || got.LowDate != c.lowDate || got.LowBill != c.lowBill {
			t.Errorf("%s: %+v; want %d on %v (%s)", c.name, got, c.want, c.lowDate, c.lowBill)
		}
	}

	// The low point names the irregular bill rather than a bigger monthly one.
	in := household(12, 26, bill(1, "Lån", 10_000, month.Monthly, 1), bill(2, "Bilforsikring", 6_000, month.Yearly(time.March), 1))
	if got, _ := FelleskontoBuffer(in, Date{2026, 11, 22}); got.LowBill != "Bilforsikring" || got.LowDate != (Date{2027, 3, 1}) {
		t.Errorf("low point = %+v, want 1 March 2027 (Bilforsikring)", got)
	}

	// Without a payday the transfers can't be dated.
	if _, err := FelleskontoBuffer(household(12, 0, bill(1, "Lån", 10_000, month.Monthly, 1)), Date{2026, 11, 22}); !errors.Is(err, ErrNoPayday) {
		t.Errorf("missing payday: err = %v", err)
	}
}
