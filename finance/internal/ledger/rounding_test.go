package ledger

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

func TestRoundHalfEven(t *testing.T) {
	cases := []struct{ num, want int64 }{ // all divided by 12
		{0, 0}, {5, 0}, {6, 0}, {7, 1}, // 0.5 → 0 (even)
		{18, 2},                    // 1.5 → 2
		{30, 2},                    // 2.5 → 2
		{42, 4},                    // 3.5 → 4
		{-5, 0}, {-6, 0}, {-7, -1}, // −0.5 → 0
		{-18, -2}, {-30, -2}, // −1.5 → −2, −2.5 → −2
		{120, 10}, {-120, -10},
	}
	for _, c := range cases {
		if got := roundHalfEven(c.num, 12); got != c.want {
			t.Errorf("roundHalfEven(%d/12) = %d, want %d", c.num, got, c.want)
		}
	}
}

func TestMonthlyEquivalent(t *testing.T) {
	cases := []struct {
		amount money.Ore
		due    month.Mask
		want   money.Ore
	}{
		{money.Kr(2_000), month.Monthly, money.Kr(2_000)},
		{money.Kr(6_000), month.Semiannual(1), money.Kr(1_000)},
		{money.Kr(12_000), month.Yearly(3), money.Kr(1_000)},
		{money.Kr(3_500), month.ElevenMonths(7), 320_833}, // 3 208,333… kr
		{6, month.Yearly(1), 0},                           // 0,5 øre → 0
		{18, month.Yearly(1), 2},                          // 1,5 øre → 2
	}
	for _, c := range cases {
		if got := MonthlyEquivalent(c.amount, c.due); got != c.want {
			t.Errorf("MonthlyEquivalent(%v, %v) = %d, want %d", c.amount, c.due.Describe(), got, c.want)
		}
	}
}

// Tiny amounts where the two halves both round the same way, so the øre left over (or
// missing) must be assigned deterministically: to the lower user ID on a tie.
func TestResidualOreAssignment(t *testing.T) {
	cases := []struct {
		amount money.Ore // per month, split 50/50, from the felleskonto
		a, b   money.Ore
	}{
		{1, 1, 0}, // 0.5 + 0.5: both round to 0, A gets the missing øre
		{3, 1, 2}, // 1.5 + 1.5: both round to 2, A gives one back
		{5, 3, 2}, // 2.5 + 2.5: both round to 2, A gets the missing øre
		{7, 3, 4}, // 3.5 + 3.5: both round to 4, A gives one back
		{4, 2, 2},
	}
	for _, c := range cases {
		in := Input{Users: users, Expenses: []Expense{sharedExp(1, "x", c.amount, month.Monthly, Felleskonto, 5000)}}
		s, err := Settle(in, m("2026-01"))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := s.User(A)
		b, _ := s.User(B)
		if a.Transfer != c.a || b.Transfer != c.b || s.Felleskonto != c.amount {
			t.Errorf("%d øre: A=%d B=%d felleskonto=%d, want %d/%d/%d", c.amount, a.Transfer, b.Transfer, s.Felleskonto, c.a, c.b, c.amount)
		}
	}
}

// The larger remainder wins the extra øre even when it belongs to the higher user ID.
func TestResidualFollowsLargestRemainder(t *testing.T) {
	// 1 øre/month split 30/70: exact 0.3 and 0.7. Rounded: 0 and 1, already summing to
	// 1, so no adjustment. Then 2 øre split 25/75: 0.5 and 1.5 → 0 and 2 (half-even),
	// summing to 2. And 1 øre split 50.01/49.99: 0.5001 → 1, 0.4999 → 0.
	cases := []struct {
		amount money.Ore
		bpA    int
		a, b   money.Ore
	}{
		{1, 3000, 0, 1},
		{2, 2500, 0, 2},
		{1, 5001, 1, 0},
		{1, 4999, 0, 1},
	}
	for _, c := range cases {
		in := Input{Users: users, Expenses: []Expense{sharedExp(1, "x", c.amount, month.Monthly, Felleskonto, c.bpA)}}
		s, _ := Settle(in, m("2026-01"))
		a, _ := s.User(A)
		b, _ := s.User(B)
		if a.Transfer != c.a || b.Transfer != c.b {
			t.Errorf("%d øre at %d bp: A=%d B=%d, want %d/%d", c.amount, c.bpA, a.Transfer, b.Transfer, c.a, c.b)
		}
	}
}

// Leftover is rounded once, half-even, from its exact value.
func TestLeftoverRoundsHalfEven(t *testing.T) {
	for _, c := range []struct{ income, want money.Ore }{
		{1000, 1000}, // 999,5 → 1000
		{1001, 1000}, // 1000,5 → 1000
		{1002, 1002}, // 1001,5 → 1002
	} {
		in := Input{
			Users:    users,
			Incomes:  incomes(c.income, 0),
			Expenses: []Expense{{ID: 1, Kind: Private, Owner: A, Versions: []ExpenseVersion{{ValidFrom: m("2026-01"), Amount: 6, Due: month.Yearly(1)}}}},
		}
		s, _ := Settle(in, m("2026-05"))
		if a, _ := s.User(A); a.Leftover != c.want {
			t.Errorf("income %d − 0,5 øre: leftover %d, want %d", c.income, a.Leftover, c.want)
		}
	}
}

// Randomized invariants over many generated households (fixed seed, so reproducible).
func TestSettlementInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(2026, 11))
	for iter := 0; iter < 3000; iter++ {
		ids := []UserID{A, B}
		if iter%5 == 0 {
			ids = append(ids, 3) // the math is not limited to two users
		}
		in := Input{Users: ids, Incomes: map[UserID][]IncomeVersion{}}
		for _, u := range ids {
			in.Incomes[u] = []IncomeVersion{{ValidFrom: m("2026-01"), Amount: money.Ore(rng.Int64N(int64(money.Kr(100_000))))}}
		}
		for e := 0; e < 1+rng.IntN(10); e++ {
			amount := money.Ore(rng.Int64N(int64(money.Kr(20_000))))
			if rng.IntN(10) == 0 {
				amount = money.Ore(rng.Int64N(int64(money.Max) + 1))
			}
			v := ExpenseVersion{ValidFrom: m("2026-01"), Amount: amount, Due: month.Mask(1 + rng.IntN(int(month.Monthly)))}
			exp := Expense{ID: int64(e + 1), Name: "x", Kind: Shared}
			if rng.IntN(4) == 0 {
				exp.Kind, exp.Owner = Private, ids[rng.IntN(len(ids))]
			} else {
				if p := rng.IntN(len(ids) + 2); p < len(ids) {
					v.Payer = ids[p]
				}
				left := 10000
				for i, u := range ids {
					bp := left
					if i < len(ids)-1 {
						bp = rng.IntN(left + 1)
					}
					v.Shares = append(v.Shares, Share{u, bp})
					left -= bp
				}
			}
			exp.Versions = []ExpenseVersion{v}
			in.Expenses = append(in.Expenses, exp)
		}

		s, err := Settle(in, m("2026-01").Add(rng.IntN(12)))
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}

		// Independent exact computation of the felleskonto inflow and each net
		// transfer, in units of 1/120000 øre.
		var fkExact int64
		netExact := map[UserID]int64{}
		for _, e := range in.Expenses {
			v := e.Versions[0]
			perYear := int64(v.Amount) * int64(v.Due.Count())
			if e.Kind != Shared {
				continue
			}
			for _, sh := range v.Shares {
				netExact[sh.User] += perYear * int64(sh.BP)
			}
			if v.Payer == Felleskonto {
				fkExact += perYear * 10000
			} else {
				netExact[v.Payer] -= perYear * 10000
			}
		}
		if diff := int64(s.Felleskonto)*den - fkExact; diff > den/2 || diff < -den/2 {
			t.Fatalf("iter %d: felleskonto %d is not the rounded exact value %d/%d", iter, s.Felleskonto, fkExact, den)
		}

		var sumTransfer, sumTo money.Ore
		owedTo := map[UserID]money.Ore{}
		paidBy := map[UserID]money.Ore{}
		for _, r := range s.Reimbursements {
			if r.Amount <= 0 || r.From == r.To {
				t.Fatalf("iter %d: bad reimbursement %+v", iter, r)
			}
			owedTo[r.To] += r.Amount
			paidBy[r.From] += r.Amount
		}
		for _, u := range s.Users {
			sumTransfer += u.Transfer
			sumTo += u.ToFelleskonto
			if u.ToFelleskonto < 0 {
				t.Fatalf("iter %d: user %d ToFelleskonto %d < 0", iter, u.User, u.ToFelleskonto)
			}
			if diff := int64(u.Transfer)*den - netExact[u.User]; diff >= den || diff <= -den {
				t.Fatalf("iter %d: user %d transfer %d more than 1 øre from exact %d/%d", iter, u.User, u.Transfer, netExact[u.User], den)
			}
			if u.Transfer < 0 && owedTo[u.User] != -u.Transfer {
				t.Fatalf("iter %d: user %d owed %d but reimbursed %d", iter, u.User, -u.Transfer, owedTo[u.User])
			}
			if u.Transfer >= 0 && u.ToFelleskonto+paidBy[u.User] != u.Transfer {
				t.Fatalf("iter %d: user %d transfer %d ≠ to felleskonto %d + reimbursements %d", iter, u.User, u.Transfer, u.ToFelleskonto, paidBy[u.User])
			}
		}
		if sumTransfer != s.Felleskonto || sumTo != s.Felleskonto {
			t.Fatalf("iter %d: Σtransfer=%d ΣtoFelleskonto=%d felleskonto=%d", iter, sumTransfer, sumTo, s.Felleskonto)
		}
	}
}

func TestValidate(t *testing.T) {
	good := func() Input {
		return Input{
			Users:   users,
			Incomes: incomes(money.Kr(1), money.Kr(1)),
			Expenses: []Expense{
				sharedExp(1, "Husleie", money.Kr(100), month.Monthly, Felleskonto, 5000),
				privateExp(2, "Sparing", A, money.Kr(100)),
			},
		}
	}
	if err := Validate(good()); err != nil {
		t.Fatalf("good input rejected: %v", err)
	}
	cases := map[string]func(*Input){
		"no users":              func(in *Input) { in.Users = nil },
		"duplicate user":        func(in *Input) { in.Users = []UserID{A, A} },
		"zero user id":          func(in *Input) { in.Users = []UserID{0, B} },
		"income unknown user":   func(in *Input) { in.Incomes[9] = []IncomeVersion{{ValidFrom: m("2026-01")}} },
		"duplicate income from": func(in *Input) { in.Incomes[A] = append(in.Incomes[A], in.Incomes[A][0]) },
		"negative income":       func(in *Input) { in.Incomes[A][0].Amount = -1 },
		"shares sum 9999":       func(in *Input) { in.Expenses[0].Versions[0].Shares[0].BP = 4999 },
		"share unknown user":    func(in *Input) { in.Expenses[0].Versions[0].Shares[1].User = 9 },
		"duplicate share user":  func(in *Input) { in.Expenses[0].Versions[0].Shares[1].User = A },
		"share over 100%":       func(in *Input) { in.Expenses[0].Versions[0].Shares = []Share{{A, 10001}, {B, -1}} },
		"payer unknown":         func(in *Input) { in.Expenses[0].Versions[0].Payer = 9 },
		"shared with owner":     func(in *Input) { in.Expenses[0].Owner = A },
		"private unknown owner": func(in *Input) { in.Expenses[1].Owner = 9 },
		"private with shares":   func(in *Input) { in.Expenses[1].Versions[0].Shares = []Share{{A, 10000}} },
		"private with payer":    func(in *Input) { in.Expenses[1].Versions[0].Payer = A },
		"unknown kind":          func(in *Input) { in.Expenses[0].Kind = "felles" },
		"no versions":           func(in *Input) { in.Expenses[0].Versions = nil },
		"empty due mask":        func(in *Input) { in.Expenses[0].Versions[0].Due = 0 },
		"due mask bit 12":       func(in *Input) { in.Expenses[0].Versions[0].Due = 1 << 12 },
		"negative amount":       func(in *Input) { in.Expenses[0].Versions[0].Amount = -1 },
		"amount over max":       func(in *Input) { in.Expenses[0].Versions[0].Amount = money.Max + 1 },
		"zero valid_from":       func(in *Input) { in.Expenses[0].Versions[0].ValidFrom = 0 },
		"duplicate valid_from": func(in *Input) {
			in.Expenses[0].Versions = append(in.Expenses[0].Versions, in.Expenses[0].Versions[0])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := good()
			mutate(&in)
			err := Validate(in)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.HasPrefix(err.Error(), "ledger: ") {
				t.Errorf("error %q lacks package prefix", err)
			}
			if _, err := Settle(in, m("2026-01")); err == nil {
				t.Error("Settle accepted invalid input")
			}
		})
	}
}
