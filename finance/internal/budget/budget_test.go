package budget

import (
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

func d(y int, m time.Month, day int) Date { return Date{y, m, day} }

func TestEaster(t *testing.T) {
	for year, want := range map[int]Date{
		2024: d(2024, 3, 31), 2025: d(2025, 4, 20), 2026: d(2026, 4, 5), 2027: d(2027, 3, 28), 2028: d(2028, 4, 16),
	} {
		if got := Easter(year); got != want {
			t.Errorf("Easter(%d) = %v, want %v", year, got, want)
		}
	}
}

func TestBankHolidays2026(t *testing.T) {
	holidays := []Date{
		d(2026, 1, 1), d(2026, 4, 2), d(2026, 4, 3), d(2026, 4, 6), d(2026, 5, 1), d(2026, 5, 14),
		d(2026, 5, 17), d(2026, 5, 25), d(2026, 12, 24), d(2026, 12, 25), d(2026, 12, 26), d(2026, 12, 31),
	}
	for _, h := range holidays {
		if !IsBankHoliday(h) {
			t.Errorf("%v should be a bank holiday", h)
		}
	}
	for _, w := range []Date{d(2026, 4, 1), d(2026, 4, 7), d(2026, 5, 13), d(2026, 12, 23), d(2026, 12, 27)} {
		if IsBankHoliday(w) {
			t.Errorf("%v should not be a bank holiday", w)
		}
	}
}

func TestPayDate(t *testing.T) {
	for _, c := range []struct {
		name    string
		nominal string
		payday  int
		want    Date
	}{
		{"weekday stays", "2026-11", 20, d(2026, 11, 20)},
		{"Sunday → Friday", "2026-12", 20, d(2026, 12, 18)},
		{"17 May (Monday) → Friday 14th", "2027-05", 17, d(2027, 5, 14)},
		{"Maundy Thursday → Wednesday", "2027-03", 25, d(2027, 3, 24)},
		{"Christmas Eve → 23rd", "2026-12", 24, d(2026, 12, 23)},
		{"1 January → 30 December the year before", "2027-01", 1, d(2026, 12, 30)},
		{"31st in February, non-leap (Sunday 28th) → Friday", "2027-02", 31, d(2027, 2, 26)},
		{"31st in February, leap year", "2028-02", 31, d(2028, 2, 29)},
		{"31st in a 30-day month", "2026-11", 31, d(2026, 11, 30)},
	} {
		if got := PayDate(month.MustParse(c.nominal), c.payday); got != c.want {
			t.Errorf("%s: PayDate(%s, %d) = %v, want %v", c.name, c.nominal, c.payday, got, c.want)
		}
	}
}

func TestPeriodAt(t *testing.T) {
	for _, c := range []struct {
		today          Date
		payday         int
		start, next    Date
		budget         string
		days, daysLeft int
	}{
		{d(2026, 11, 25), 20, d(2026, 11, 20), d(2026, 12, 18), "2026-11", 28, 23},
		{d(2026, 11, 20), 20, d(2026, 11, 20), d(2026, 12, 18), "2026-11", 28, 28}, // payday itself
		{d(2026, 11, 19), 20, d(2026, 10, 20), d(2026, 11, 20), "2026-10", 31, 1},  // day before
		{d(2026, 12, 19), 20, d(2026, 12, 18), d(2027, 1, 20), "2026-12", 33, 32},  // after an early (weekend-moved) payday
		{d(2026, 12, 30), 1, d(2026, 12, 30), d(2027, 2, 1), "2027-01", 33, 33},    // January's salary paid in December
		{d(2026, 12, 29), 1, d(2026, 12, 1), d(2026, 12, 30), "2026-12", 29, 1},
	} {
		p := PeriodAt(c.today, c.payday)
		if p.Start != c.start || p.Next != c.next || p.BudgetMonth != month.MustParse(c.budget) || p.Days() != c.days || p.DaysLeft(c.today) != c.daysLeft {
			t.Errorf("PeriodAt(%v, %d) = %v→%v budget %v, %d days, %d left; want %v→%v budget %s, %d, %d",
				c.today, c.payday, p.Start, p.Next, p.BudgetMonth, p.Days(), p.DaysLeft(c.today), c.start, c.next, c.budget, c.days, c.daysLeft)
		}
	}
}

func TestShouldHave(t *testing.T) {
	p := PeriodAt(d(2026, 11, 25), 20) // 20 Nov → 18 Dec, 28 days
	for _, c := range []struct {
		today Date
		want  money.Ore
	}{
		{d(2026, 11, 20), money.Kr(10_000)}, // payday: all of it
		{d(2026, 11, 25), 821_429},          // 10 000 × 23/28 = 8 214,2857… kr
		{d(2026, 12, 17), 35_714},           // last day: one day's worth
	} {
		if got := ShouldHave(money.Kr(10_000), p, c.today); got != c.want {
			t.Errorf("ShouldHave on %v = %d, want %d", c.today, got, c.want)
		}
	}
}

func TestAdvise(t *testing.T) {
	today := d(2026, 11, 25)
	p := PeriodAt(today, 20) // 28 days, 23 left
	leftover := money.Kr(10_000)
	for _, c := range []struct {
		name            string
		balance, price  money.Ore
		leftover        money.Ore
		want            Verdict
		perDay, missing money.Ore
		big             bool
	}{
		{"ahead of pace", money.Kr(10_000), money.Kr(1_000), leftover, Fine, 39_130, 0, false},
		{"exactly on pace", 821_429 + money.Kr(500), money.Kr(500), leftover, Fine, 35_714, 0, false},
		{"behind but at least half a normal day", money.Kr(6_000), money.Kr(1_000), leftover, Tight, 21_739, 0, false},
		{"under half a normal day", money.Kr(5_000), money.Kr(2_000), leftover, RatherNot, 13_043, 0, false},
		{"balance doesn't cover it", money.Kr(500), money.Kr(800), leftover, NotCovered, 0, money.Kr(300), false},
		{"budget doesn't add up", money.Kr(1_000), money.Kr(10), -money.Kr(50), NoBudget, 4_304, 0, false},
		{"bigger than a month's free money", money.Kr(20_000), money.Kr(12_000), leftover, Tight, 34_783, 0, true},
	} {
		a := Advise(c.balance, c.price, c.leftover, p, today)
		if a.Verdict != c.want || a.PerDay != c.perDay || a.Missing != c.missing || a.BigPurchase != c.big || a.DaysLeft != 23 {
			t.Errorf("%s: %+v; want verdict %d, per day %d, missing %d, big %v", c.name, a, c.want, c.perDay, c.missing, c.big)
		}
	}
	if a := Advise(money.Kr(9_000), 0, leftover, p, today); a.NormalPerDay != 35_714 || a.ShouldHave != 821_429 {
		t.Errorf("normal per day %d, should have %d", a.NormalPerDay, a.ShouldHave)
	}
}
