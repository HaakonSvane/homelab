package ledger

import (
	"fmt"
	"sort"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// den is the denominator of the exact intermediate sums: amounts are accumulated in
// units of 1/120000 øre (12 months × 10000 basis points), so smoothing (×n/12) and
// splitting (×bp/10000) never lose precision. Rounding happens once, at the end.
const den = 12 * 10000

// Settle computes the settlement for month m.
func Settle(in Input, m month.Month) (Settlement, error) {
	if err := Validate(in); err != nil {
		return Settlement{}, err
	}
	n := len(in.Users)
	idx := make(map[UserID]int, n)
	for i, u := range in.Users {
		idx[u] = i
	}

	// Exact sums, in units of 1/den øre.
	obligation := make([]int64, n)
	paidDirect := make([]int64, n)
	private := make([]int64, n)
	var felleskonto int64
	var due []DueItem

	for _, e := range in.Expenses {
		v, ok := e.At(m)
		if !ok {
			continue
		}
		perYear := int64(v.Amount) * int64(v.Due.Count()) // øre per year; ×den/12 = units per month
		switch e.Kind {
		case Shared:
			for _, s := range v.Shares {
				obligation[idx[s.User]] += perYear * int64(s.BP)
			}
			if v.Payer == Felleskonto {
				felleskonto += perYear * 10000
			} else {
				paidDirect[idx[v.Payer]] += perYear * 10000
			}
		case Private:
			private[idx[e.Owner]] += perYear * 10000
		}
		if v.Due.Has(m.Month()) && v.Amount > 0 {
			due = append(due, DueItem{
				ExpenseID: e.ID, Name: e.Name, Category: e.Category, Kind: e.Kind,
				Owner: e.Owner, Payer: v.Payer, Amount: v.Amount,
			})
		}
	}

	s := Settlement{Month: m, Felleskonto: money.Ore(roundHalfEven(felleskonto, den))}

	net := make([]int64, n)
	for i := range net {
		net[i] = obligation[i] - paidDirect[i]
	}
	transfers := apportion(net, int64(s.Felleskonto), in.Users)

	s.Users = make([]UserResult, n)
	for i, u := range in.Users {
		var income money.Ore
		if v, ok := IncomeAt(in.Incomes[u], m); ok {
			income = v.Amount
		}
		s.Users[i] = UserResult{
			User:          u,
			Income:        income,
			Obligation:    money.Ore(roundHalfEven(obligation[i], den)),
			PaidDirect:    money.Ore(roundHalfEven(paidDirect[i], den)),
			Private:       money.Ore(roundHalfEven(private[i], den)),
			Transfer:      money.Ore(transfers[i]),
			ToFelleskonto: money.Ore(max(transfers[i], 0)),
			Leftover:      money.Ore(roundHalfEven(int64(income)*den-obligation[i]-private[i], den)),
		}
	}

	// A negative transfer means the user pays more shared bills directly than their
	// share. Instead of the felleskonto paying them back, the other user(s) pay them
	// directly, out of what they would otherwise have sent to the felleskonto. The
	// positive transfers always cover this, since all transfers sum to Felleskonto ≥ 0.
	for i := range s.Users {
		owed := -s.Users[i].Transfer
		for j := range s.Users {
			if owed <= 0 {
				break
			}
			amt := min(owed, s.Users[j].ToFelleskonto)
			if amt <= 0 {
				continue
			}
			s.Users[j].ToFelleskonto -= amt
			owed -= amt
			s.Reimbursements = append(s.Reimbursements, Reimbursement{From: s.Users[j].User, To: s.Users[i].User, Amount: amt})
		}
	}

	sort.SliceStable(due, func(a, b int) bool {
		if due[a].Amount != due[b].Amount {
			return due[a].Amount > due[b].Amount
		}
		if due[a].Name != due[b].Name {
			return due[a].Name < due[b].Name
		}
		return due[a].ExpenseID < due[b].ExpenseID
	})
	s.Due = due
	return s, nil
}

// apportion rounds each exact value (in units of 1/den øre) half-even, then hands out
// whatever øre are left over or missing relative to target by largest remainder: the
// users whose rounding moved them furthest in the opposite direction absorb the
// difference first, ties going to the lower user ID. The result always sums to target.
func apportion(exact []int64, target int64, ids []UserID) []int64 {
	n := len(exact)
	out := make([]int64, n)
	rem := make([]int64, n) // exact − rounded, in units
	var sum int64
	for i, x := range exact {
		out[i] = roundHalfEven(x, den)
		rem[i] = x - out[i]*den
		sum += out[i]
	}
	residual := target - sum
	if residual == 0 || n == 0 {
		return out
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := rem[order[a]], rem[order[b]]
		if ra != rb {
			if residual > 0 {
				return ra > rb // rounded down the most → gets an extra øre
			}
			return ra < rb // rounded up the most → gives one back
		}
		return ids[order[a]] < ids[order[b]]
	})
	for k := 0; residual != 0; k++ {
		i := order[k%n]
		if residual > 0 {
			out[i]++
			residual--
		} else {
			out[i]--
			residual++
		}
	}
	return out
}

// roundHalfEven returns num/d rounded to the nearest integer, ties to even (d > 0).
func roundHalfEven(num, d int64) int64 {
	q, r := num/d, num%d
	if r < 0 { // floor division
		q--
		r += d
	}
	switch {
	case 2*r > d:
		q++
	case 2*r == d && q%2 != 0:
		q++
	}
	return q
}

// MonthlyEquivalent is the smoothed monthly cost of a bill: amount × (due months)/12,
// rounded half-even. For display; Settle sums exactly and rounds only at the end.
func MonthlyEquivalent(amount money.Ore, due month.Mask) money.Ore {
	return money.Ore(roundHalfEven(int64(amount)*int64(due.Count()), 12))
}

// At returns the version in effect in month m: the one with the greatest ValidFrom
// that is not after m. False if the expense has not started or has ended by m.
func (e Expense) At(m month.Month) (ExpenseVersion, bool) {
	if !e.End.IsZero() && m > e.End {
		return ExpenseVersion{}, false
	}
	return effective(e.Versions, func(v ExpenseVersion) month.Month { return v.ValidFrom }, m)
}

// IncomeAt returns the income version in effect in month m, if any.
func IncomeAt(versions []IncomeVersion, m month.Month) (IncomeVersion, bool) {
	return effective(versions, func(v IncomeVersion) month.Month { return v.ValidFrom }, m)
}

func effective[V any](versions []V, validFrom func(V) month.Month, m month.Month) (V, bool) {
	var best V
	var bestFrom month.Month
	found := false
	for _, v := range versions {
		if f := validFrom(v); f <= m && (!found || f > bestFrom) {
			best, bestFrom, found = v, f, true
		}
	}
	return best, found
}

// Validate checks the invariants Settle relies on. The store and web layers enforce
// the same rules on write, so a failure here means corrupt data.
func Validate(in Input) error {
	if len(in.Users) == 0 {
		return fmt.Errorf("ledger: no users")
	}
	known := make(map[UserID]bool, len(in.Users))
	for _, u := range in.Users {
		if u <= 0 || known[u] {
			return fmt.Errorf("ledger: invalid or duplicate user id %d", u)
		}
		known[u] = true
	}
	for u, versions := range in.Incomes {
		if !known[u] {
			return fmt.Errorf("ledger: income for unknown user %d", u)
		}
		seen := map[month.Month]bool{}
		for _, v := range versions {
			if v.ValidFrom.IsZero() || seen[v.ValidFrom] {
				return fmt.Errorf("ledger: user %d: missing or duplicate income valid_from %v", u, v.ValidFrom)
			}
			seen[v.ValidFrom] = true
			if v.Amount < 0 || v.Amount > money.Max {
				return fmt.Errorf("ledger: user %d: income %d øre out of range", u, v.Amount)
			}
		}
	}
	for _, e := range in.Expenses {
		if err := ValidateExpense(e, in.Users); err != nil {
			return err
		}
	}
	return nil
}

// ValidateExpense checks one expense and all its versions against the user set.
func ValidateExpense(e Expense, users []UserID) error {
	known := make(map[UserID]bool, len(users))
	for _, u := range users {
		known[u] = true
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("ledger: expense %d (%s): %s", e.ID, e.Name, fmt.Sprintf(format, args...))
	}
	switch e.Kind {
	case Shared:
		if e.Owner != 0 {
			return fail("shared expense has an owner")
		}
	case Private:
		if !known[e.Owner] {
			return fail("private expense owner %d is not a user", e.Owner)
		}
	default:
		return fail("unknown kind %q", e.Kind)
	}
	if len(e.Versions) == 0 {
		return fail("no versions")
	}
	seen := map[month.Month]bool{}
	for _, v := range e.Versions {
		if v.ValidFrom.IsZero() || seen[v.ValidFrom] {
			return fail("missing or duplicate valid_from %v", v.ValidFrom)
		}
		seen[v.ValidFrom] = true
		if v.Amount < 0 || v.Amount > money.Max {
			return fail("amount %d øre out of range", v.Amount)
		}
		if !v.Due.Valid() {
			return fail("version %v: due mask %b invalid", v.ValidFrom, v.Due)
		}
		if e.Kind == Private {
			if v.Payer != Felleskonto || len(v.Shares) > 0 {
				return fail("version %v: private expense with payer or shares", v.ValidFrom)
			}
			continue
		}
		if v.Payer != Felleskonto && !known[v.Payer] {
			return fail("version %v: payer %d is not a user", v.ValidFrom, v.Payer)
		}
		sum := 0
		shareSeen := map[UserID]bool{}
		for _, s := range v.Shares {
			if !known[s.User] || shareSeen[s.User] || s.BP < 0 || s.BP > 10000 {
				return fail("version %v: invalid share %+v", v.ValidFrom, s)
			}
			shareSeen[s.User] = true
			sum += s.BP
		}
		if sum != 10000 {
			return fail("version %v: shares sum to %d bp, want 10000", v.ValidFrom, sum)
		}
	}
	return nil
}
