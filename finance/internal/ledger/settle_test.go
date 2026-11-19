package ledger

import (
	"reflect"
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

const (
	A UserID = 1
	B UserID = 2
)

var users = []UserID{A, B}

func m(s string) month.Month { return month.MustParse(s) }

// sharedExp is a shared expense with one version from January 2026.
func sharedExp(id int64, name string, amount money.Ore, due month.Mask, payer UserID, bpA int) Expense {
	return Expense{ID: id, Name: name, Kind: Shared, Versions: []ExpenseVersion{
		{ValidFrom: m("2026-01"), Amount: amount, Due: due, Payer: payer, Shares: []Share{{A, bpA}, {B, 10000 - bpA}}},
	}}
}

func privateExp(id int64, name string, owner UserID, amount money.Ore) Expense {
	return Expense{ID: id, Name: name, Category: "Sparing", Kind: Private, Owner: owner, Versions: []ExpenseVersion{
		{ValidFrom: m("2026-01"), Amount: amount, Due: month.Monthly},
	}}
}

func incomes(a, b money.Ore) map[UserID][]IncomeVersion {
	return map[UserID][]IncomeVersion{
		A: {{ValidFrom: m("2026-01"), Amount: a}},
		B: {{ValidFrom: m("2026-01"), Amount: b}},
	}
}

// want lists the figures checked per user; zero-valued fields are checked too.
type want struct {
	transfer, toFelleskonto, obligation, paidDirect, private, leftover money.Ore
}

func check(t *testing.T, s Settlement, felleskonto money.Ore, perUser map[UserID]want, reimb []Reimbursement) {
	t.Helper()
	if s.Felleskonto != felleskonto {
		t.Errorf("Felleskonto = %v, want %v", s.Felleskonto, felleskonto)
	}
	for id, w := range perUser {
		u, ok := s.User(id)
		if !ok {
			t.Fatalf("no result for user %d", id)
		}
		got := want{u.Transfer, u.ToFelleskonto, u.Obligation, u.PaidDirect, u.Private, u.Leftover}
		if got != w {
			t.Errorf("user %d:\n got %+v\nwant %+v", id, got, w)
		}
	}
	if !reflect.DeepEqual(s.Reimbursements, reimb) {
		t.Errorf("Reimbursements = %+v, want %+v", s.Reimbursements, reimb)
	}
}

// The required case from the brief: shared costs of 20 000 kr/month, of which
// electricity (2 000 kr, split 50/50) is paid directly by A and the rest goes through
// the felleskonto. A transfers 8 000, B 10 000, and the felleskonto receives 18 000.
func TestRequiredCase(t *testing.T) {
	in := Input{
		Users:   users,
		Incomes: incomes(money.Kr(50_000), money.Kr(45_000)),
		Expenses: []Expense{
			sharedExp(1, "Husleie", money.Kr(14_000), month.Monthly, Felleskonto, 5000),
			sharedExp(2, "Mat", money.Kr(4_000), month.Monthly, Felleskonto, 5000),
			sharedExp(3, "Strøm", money.Kr(2_000), month.Monthly, A, 5000),
		},
	}
	s, err := Settle(in, m("2026-11"))
	if err != nil {
		t.Fatal(err)
	}
	check(t, s, money.Kr(18_000), map[UserID]want{
		A: {transfer: money.Kr(8_000), toFelleskonto: money.Kr(8_000), obligation: money.Kr(10_000), paidDirect: money.Kr(2_000), leftover: money.Kr(40_000)},
		B: {transfer: money.Kr(10_000), toFelleskonto: money.Kr(10_000), obligation: money.Kr(10_000), leftover: money.Kr(35_000)},
	}, nil)
}

func TestUnevenSplitAndPayerB(t *testing.T) {
	in := Input{
		Users:   users,
		Incomes: incomes(money.Kr(40_000), money.Kr(30_000)),
		Expenses: []Expense{
			sharedExp(1, "Husleie", money.Kr(10_000), month.Monthly, Felleskonto, 6000),
			sharedExp(2, "Internett", money.Kr(500), month.Monthly, B, 5000),
		},
	}
	s, err := Settle(in, m("2026-05"))
	if err != nil {
		t.Fatal(err)
	}
	check(t, s, money.Kr(10_000), map[UserID]want{
		A: {transfer: money.Kr(6_250), toFelleskonto: money.Kr(6_250), obligation: money.Kr(6_250), leftover: money.Kr(33_750)},
		B: {transfer: money.Kr(3_750), toFelleskonto: money.Kr(3_750), obligation: money.Kr(4_250), paidDirect: money.Kr(500), leftover: money.Kr(25_750)},
	}, nil)
}

// Irregular bills are smoothed: municipal tax (2 bits), kindergarten (11 bits) and a
// yearly insurance (1 bit) each cost amount×n/12 every month, while the due list
// shows the real amount only in the months the bill arrives.
func TestDueMasksAreSmoothed(t *testing.T) {
	in := Input{
		Users:   users,
		Incomes: incomes(money.Kr(50_000), money.Kr(50_000)),
		Expenses: []Expense{
			sharedExp(1, "Kommunale avgifter", money.Kr(6_000), month.Semiannual(time.January), Felleskonto, 5000),
			sharedExp(2, "Barnehage", money.Kr(3_500), month.ElevenMonths(time.July), Felleskonto, 5000),
			sharedExp(3, "Innboforsikring", money.Kr(12_000), month.Yearly(time.March), Felleskonto, 5000),
		},
	}
	// 1 000 + 3 208,333… + 1 000 = 5 208,333… kr → 520 833 øre. Each half is
	// 260 416,67 øre; both round up to 260 417, one øre too many, and the tie on
	// remainders goes to the lower user ID.
	for _, mo := range []string{"2026-01", "2026-03", "2026-07", "2026-12"} {
		s, err := Settle(in, m(mo))
		if err != nil {
			t.Fatal(err)
		}
		check(t, s, 520_833, map[UserID]want{
			A: {transfer: 260_416, toFelleskonto: 260_416, obligation: 260_417, leftover: 5_000_000 - 260_417},
			B: {transfer: 260_417, toFelleskonto: 260_417, obligation: 260_417, leftover: 5_000_000 - 260_417},
		}, nil)
	}

	dueNames := func(mo string) []string {
		s, _ := Settle(in, m(mo))
		var names []string
		for _, d := range s.Due {
			names = append(names, d.Name)
		}
		return names
	}
	for mo, wantNames := range map[string][]string{
		"2026-01": {"Kommunale avgifter", "Barnehage"},
		"2026-03": {"Innboforsikring", "Barnehage"},
		"2026-07": {"Kommunale avgifter"}, // no kindergarten bill in July
		"2026-08": {"Barnehage"},
	} {
		if got := dueNames(mo); !reflect.DeepEqual(got, wantNames) {
			t.Errorf("due in %s = %v, want %v", mo, got, wantNames)
		}
	}
	s, _ := Settle(in, m("2026-03"))
	if s.Due[0].Amount != money.Kr(12_000) {
		t.Errorf("due amount = %v, want the full 12 000 kr, not the smoothed amount", s.Due[0].Amount)
	}
}

// Private expenses (including savings) reduce only their owner's leftover.
func TestPrivateAffectsOnlyLeftover(t *testing.T) {
	in := Input{
		Users:   users,
		Incomes: incomes(money.Kr(40_000), money.Kr(40_000)),
		Expenses: []Expense{
			sharedExp(1, "Husleie", money.Kr(10_000), month.Monthly, Felleskonto, 5000),
			privateExp(2, "Sparing", A, money.Kr(5_000)),
		},
	}
	s, err := Settle(in, m("2026-02"))
	if err != nil {
		t.Fatal(err)
	}
	check(t, s, money.Kr(10_000), map[UserID]want{
		A: {transfer: money.Kr(5_000), toFelleskonto: money.Kr(5_000), obligation: money.Kr(5_000), private: money.Kr(5_000), leftover: money.Kr(30_000)},
		B: {transfer: money.Kr(5_000), toFelleskonto: money.Kr(5_000), obligation: money.Kr(5_000), leftover: money.Kr(35_000)},
	}, nil)
}

// Versions take effect from their valid_from month; the version list need not be sorted.
func TestVersionsChangeMidYear(t *testing.T) {
	in := Input{
		Users: users,
		Incomes: map[UserID][]IncomeVersion{
			A: {{ValidFrom: m("2026-07"), Amount: money.Kr(55_000)}, {ValidFrom: m("2026-01"), Amount: money.Kr(50_000)}},
			B: {{ValidFrom: m("2026-01"), Amount: money.Kr(45_000)}},
		},
		Expenses: []Expense{{ID: 1, Name: "Strøm", Kind: Shared, Versions: []ExpenseVersion{
			{ValidFrom: m("2026-07"), Amount: money.Kr(2_500), Due: month.Monthly, Shares: []Share{{A, 5000}, {B, 5000}}},
			{ValidFrom: m("2026-01"), Amount: money.Kr(1_500), Due: month.Monthly, Shares: []Share{{A, 5000}, {B, 5000}}},
			{ValidFrom: m("2026-10"), Amount: money.Kr(1_800), Due: month.Monthly, Payer: B, Shares: []Share{{A, 5000}, {B, 5000}}},
		}}},
	}
	cases := []struct {
		month                string
		incomeA, felleskonto money.Ore
		transferA, transferB money.Ore
	}{
		{"2025-12", 0, 0, 0, 0}, // before anything started
		{"2026-01", money.Kr(50_000), money.Kr(1_500), money.Kr(750), money.Kr(750)},
		{"2026-06", money.Kr(50_000), money.Kr(1_500), money.Kr(750), money.Kr(750)},
		{"2026-07", money.Kr(55_000), money.Kr(2_500), money.Kr(1_250), money.Kr(1_250)},
		{"2026-09", money.Kr(55_000), money.Kr(2_500), money.Kr(1_250), money.Kr(1_250)},
		// From October B pays the bill directly, so B is owed half of it by A.
		{"2026-10", money.Kr(55_000), 0, money.Kr(900), money.Kr(-900)},
		{"2027-03", money.Kr(55_000), 0, money.Kr(900), money.Kr(-900)},
	}
	for _, c := range cases {
		s, err := Settle(in, m(c.month))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := s.User(A)
		b, _ := s.User(B)
		if a.Income != c.incomeA || s.Felleskonto != c.felleskonto || a.Transfer != c.transferA || b.Transfer != c.transferB {
			t.Errorf("%s: incomeA=%v felleskonto=%v transfers=%v/%v; want %v %v %v/%v",
				c.month, a.Income, s.Felleskonto, a.Transfer, b.Transfer, c.incomeA, c.felleskonto, c.transferA, c.transferB)
		}
	}
}

// end_month is inclusive: the expense counts in its end month and not after.
func TestEndMonth(t *testing.T) {
	gym := sharedExp(1, "Treningssenter", money.Kr(400), month.Monthly, Felleskonto, 5000)
	gym.End = m("2026-06")
	later := sharedExp(2, "Strømmetjeneste", money.Kr(200), month.Monthly, Felleskonto, 5000)
	later.Versions[0].ValidFrom = m("2026-08")
	in := Input{Users: users, Expenses: []Expense{gym, later}}

	for mo, wantF := range map[string]money.Ore{
		"2026-01": money.Kr(400),
		"2026-06": money.Kr(400),
		"2026-07": 0,
		"2026-08": money.Kr(200),
	} {
		s, err := Settle(in, m(mo))
		if err != nil {
			t.Fatal(err)
		}
		if s.Felleskonto != wantF {
			t.Errorf("%s: Felleskonto = %v, want %v", mo, s.Felleskonto, wantF)
		}
		if mo == "2026-07" && len(s.Due) != 0 {
			t.Errorf("ended expense still due in July: %+v", s.Due)
		}
	}
}

// A user who pays more shared bills directly than their own share has a negative
// transfer. They send nothing to the felleskonto and the other user pays them back
// directly, so the felleskonto still receives exactly its total.
func TestNegativeTransferBecomesReimbursement(t *testing.T) {
	for _, payer := range []UserID{A, B} {
		other := map[UserID]UserID{A: B, B: A}[payer]
		in := Input{
			Users: users,
			Expenses: []Expense{
				sharedExp(1, "Mat", money.Kr(4_000), month.Monthly, Felleskonto, 5000),
				sharedExp(2, "Lån", money.Kr(6_000), month.Monthly, payer, 5000),
			},
		}
		s, err := Settle(in, m("2026-04"))
		if err != nil {
			t.Fatal(err)
		}
		check(t, s, money.Kr(4_000), map[UserID]want{
			payer: {transfer: money.Kr(-1_000), toFelleskonto: 0, obligation: money.Kr(5_000), paidDirect: money.Kr(6_000), leftover: money.Kr(-5_000)},
			other: {transfer: money.Kr(5_000), toFelleskonto: money.Kr(4_000), obligation: money.Kr(5_000), leftover: money.Kr(-5_000)},
		}, []Reimbursement{{From: other, To: payer, Amount: money.Kr(1_000)}})
	}
}

func TestSingleUser(t *testing.T) {
	in := Input{Users: []UserID{A}, Expenses: []Expense{{ID: 1, Name: "Husleie", Kind: Shared, Versions: []ExpenseVersion{
		{ValidFrom: m("2026-01"), Amount: money.Kr(9_000), Due: month.Monthly, Shares: []Share{{A, 10000}}},
	}}}}
	s, err := Settle(in, m("2026-01"))
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := s.User(A); a.Transfer != money.Kr(9_000) || s.Felleskonto != money.Kr(9_000) {
		t.Errorf("transfer %v felleskonto %v, want 9 000 kr both", a.Transfer, s.Felleskonto)
	}
}
