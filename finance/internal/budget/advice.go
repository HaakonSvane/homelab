package budget

import "github.com/HaakonSvane/homelab/finance/internal/money"

// Verdict is the answer to "kan jeg kjøpe?".
type Verdict int

const (
	// Fine: after the purchase you are still at or ahead of an even spending pace.
	Fine Verdict = iota
	// Tight: behind pace, but at least half the normal daily amount remains.
	Tight
	// RatherNot: less than half the normal daily amount would remain until payday.
	RatherNot
	// NotCovered: the balance doesn't cover the price.
	NotCovered
	// NoBudget: the balance covers it, but the month's fixed costs exceed the income,
	// so there is no free money to pace against.
	NoBudget
)

// Advice explains a verdict with the figures behind it.
type Advice struct {
	Verdict      Verdict
	After        money.Ore // balance − price
	Missing      money.Ore // NotCovered: how much is missing
	DaysLeft     int       // until payday, today included
	PerDay       money.Ore // After spread over the days left
	NormalPerDay money.Ore // the period's free money spread evenly over the period
	ShouldHave   money.Ore // what an even pace would leave today
	Leftover     money.Ore // the period's free money
	BigPurchase  bool      // the price is more than a whole period's free money
}

// Advise says whether a purchase of price fits, given the balance on the spending
// account (after fixed payments) and the period's free money (leftover).
func Advise(balance, price, leftover money.Ore, p Period, today Date) Advice {
	a := Advice{
		After:        balance - price,
		DaysLeft:     p.DaysLeft(today),
		NormalPerDay: money.DivRound(leftover, int64(p.Days())),
		ShouldHave:   ShouldHave(leftover, p, today),
		Leftover:     leftover,
		BigPurchase:  leftover > 0 && price > leftover,
	}
	if a.After >= 0 {
		a.PerDay = money.DivRound(a.After, int64(a.DaysLeft))
	}
	switch {
	case a.After < 0:
		a.Verdict, a.Missing = NotCovered, -a.After
	case leftover <= 0:
		a.Verdict = NoBudget
	case a.After >= a.ShouldHave:
		a.Verdict = Fine
	case 2*a.PerDay >= a.NormalPerDay:
		a.Verdict = Tight
	default:
		a.Verdict = RatherNot
	}
	return a
}
