package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

var ctx = context.Background()

func twoUsers(t *testing.T, s *Store) (User, User) {
	t.Helper()
	a, err := s.CreateUser(ctx, "kari", "Kari")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser(ctx, "ola", "Ola")
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestCreateUser(t *testing.T) {
	s := newStore(t)
	a, err := s.CreateUser(ctx, "kari", "  Kari  ")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.Name != "kari" || a.DisplayName != "Kari" || !a.CreatedAt.Equal(testNow) {
		t.Errorf("created %+v", a)
	}
	if got, err := s.UserByName(ctx, "kari"); err != nil || got != a {
		t.Errorf("UserByName = %+v, %v; want %+v", got, err, a)
	}
	if got, err := s.UserByID(ctx, a.ID); err != nil || got != a {
		t.Errorf("UserByID = %+v, %v", got, err)
	}
	if _, err := s.UserByName(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user err = %v, want ErrNotFound", err)
	}

	for _, c := range []struct{ name, display string }{
		{"kari", "Kari igjen"}, // duplicate
		{"Kari", "Kari"},       // uppercase
		{"1kari", "Kari"},
		{"", "Kari"},
		{"ka ri", "Kari"},
		{"kari2", "   "},
	} {
		if _, err := s.CreateUser(ctx, c.name, c.display); !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateUser(%q, %q) err = %v, want ErrInvalid", c.name, c.display, err)
		}
	}
	// Quote marks around a name (smart quotes from a copied command) are dropped.
	ola, err := s.CreateUser(ctx, "ola", "“Ola”")
	if err != nil || ola.DisplayName != "Ola" {
		t.Fatalf("CreateUser with smart quotes = %+v, %v", ola, err)
	}
	if _, err := s.CreateUser(ctx, "per", "Per"); !errors.Is(err, ErrInvalid) {
		t.Errorf("third user err = %v, want ErrInvalid", err)
	}
	if us, _ := s.Users(ctx); len(us) != 2 || us[0].Name != "kari" || us[1].Name != "ola" {
		t.Errorf("Users = %+v", us)
	}

	for in, want := range map[string]string{"\"Kari Nordmann\"": "Kari Nordmann", "‘Kari’": "Kari", " «Kari» ": "Kari", "Kari's": "Kari's"} {
		if u, err := s.RenameUser(ctx, "kari", in); err != nil || u.DisplayName != want {
			t.Errorf("RenameUser(%q) = %q, %v; want %q", in, u.DisplayName, err, want)
		}
	}
	if _, err := s.RenameUser(ctx, "kari", "“”"); !errors.Is(err, ErrInvalid) {
		t.Errorf("rename to only quotes: %v", err)
	}
	if _, err := s.RenameUser(ctx, "nobody", "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename unknown user: %v", err)
	}
}

func TestSetIncomeReplacesOnlyItsOwnMonth(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	set := func(from string, kr int64, note string, by User) error {
		return s.SetIncome(ctx, a.ID, ledger.IncomeVersion{ValidFrom: month.MustParse(from), Amount: money.Kr(kr)}, note, by.ID)
	}
	for _, err := range []error{
		set("2026-01", 40_000, "", a),
		set("2026-07", 45_000, "", a),
		set("2026-07", 46_000, " rettet ", b), // correction of the July version
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Incomes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d versions, want 2: %+v", len(got), got)
	}
	if got[0].ValidFrom != month.MustParse("2026-01") || got[0].Amount != money.Kr(40_000) || got[0].CreatedBy != a.ID {
		t.Errorf("January version changed: %+v", got[0])
	}
	if got[1].Amount != money.Kr(46_000) || got[1].Note != "rettet" || got[1].CreatedBy != b.ID {
		t.Errorf("July version not replaced: %+v", got[1])
	}
	if got[0].Payday != 0 {
		t.Errorf("payday should be unset by default: %+v", got[0])
	}

	// Payday is stored with the version and replaced with it.
	for _, day := range []int{20, 25} {
		if err := s.SetIncome(ctx, a.ID, ledger.IncomeVersion{ValidFrom: month.MustParse("2026-07"), Amount: money.Kr(46_000), Payday: day}, "", a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.Incomes(ctx); got[1].Payday != 25 || got[0].Payday != 0 {
		t.Errorf("paydays = %d, %d; want 0, 25", got[0].Payday, got[1].Payday)
	}
	if err := s.SetIncome(ctx, a.ID, ledger.IncomeVersion{ValidFrom: month.MustParse("2026-08"), Payday: 32}, "", a.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("payday 32: err = %v, want ErrInvalid", err)
	}

	bad := []error{
		s.SetIncome(ctx, 99, ledger.IncomeVersion{ValidFrom: month.MustParse("2026-01")}, "", a.ID),
		s.SetIncome(ctx, a.ID, ledger.IncomeVersion{ValidFrom: month.MustParse("2026-01")}, "", 99),
		s.SetIncome(ctx, a.ID, ledger.IncomeVersion{ValidFrom: month.MustParse("2026-01"), Amount: -1}, "", a.ID),
		s.SetIncome(ctx, a.ID, ledger.IncomeVersion{Amount: 1}, "", a.ID),
	}
	for i, err := range bad {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("bad income %d: err = %v, want ErrInvalid", i, err)
		}
	}
}

func shares(a, b User, bpA int) []ledger.Share {
	return []ledger.Share{{User: a.ID, BP: bpA}, {User: b.ID, BP: 10000 - bpA}}
}

func TestExpenseVersionUpsert(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	jan, jun := month.MustParse("2026-01"), month.MustParse("2026-06")
	id, err := s.CreateExpense(ctx, NewExpense{Name: "Strøm", Category: "Bolig", Kind: ledger.Shared,
		First: ledger.ExpenseVersion{ValidFrom: jan, Amount: money.Kr(1_000), Due: month.Monthly, Shares: shares(a, b, 5000)}}, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExpenseVersion(ctx, id, ledger.ExpenseVersion{ValidFrom: jun, Amount: money.Kr(1_200), Due: month.Monthly, Payer: a.ID, Shares: shares(a, b, 6000)}, a.ID); err != nil {
		t.Fatal(err)
	}
	// Correct the June version: new amount, split and payer. January must not change.
	june := ledger.ExpenseVersion{ValidFrom: jun, Amount: money.Kr(1_300), Due: month.Semiannual(6), Shares: shares(a, b, 7000)}
	if err := s.SaveExpenseVersion(ctx, id, june, b.ID); err != nil {
		t.Fatal(err)
	}

	e, err := s.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	wantJan := ledger.ExpenseVersion{ValidFrom: jan, Amount: money.Kr(1_000), Due: month.Monthly, Shares: shares(a, b, 5000)}
	if !reflect.DeepEqual(e.Versions, []ledger.ExpenseVersion{wantJan, june}) {
		t.Errorf("versions = %+v", e.Versions)
	}
	if len(e.VersionMeta) != 2 || e.VersionMeta[0].CreatedBy != a.ID || e.VersionMeta[1].CreatedBy != b.ID {
		t.Errorf("version meta = %+v", e.VersionMeta)
	}
	if e.Name != "Strøm" || e.Category != "Bolig" || e.Kind != ledger.Shared || e.CreatedBy != a.ID {
		t.Errorf("expense = %+v", e)
	}

	// An invalid version is rejected and changes nothing.
	bad := june
	bad.Shares = shares(a, b, 7000)[:1]
	if err := s.SaveExpenseVersion(ctx, id, bad, a.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("shares summing to 7000: err = %v, want ErrInvalid", err)
	}
	if again, _ := s.Expense(ctx, id); !reflect.DeepEqual(again.Versions, e.Versions) {
		t.Errorf("rejected version changed data: %+v", again.Versions)
	}
	if err := s.SaveExpenseVersion(ctx, 999, june, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown expense: err = %v, want ErrNotFound", err)
	}
}

func TestCreateExpenseValidation(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	v := ledger.ExpenseVersion{ValidFrom: month.MustParse("2026-01"), Amount: money.Kr(100), Due: month.Monthly, Shares: shares(a, b, 5000)}
	pv := ledger.ExpenseVersion{ValidFrom: v.ValidFrom, Amount: v.Amount, Due: month.Monthly}
	withPayer := v
	withPayer.Payer = 99
	noDue := v
	noDue.Due = 0
	cases := map[string]NewExpense{
		"shared with owner":     {Name: "x", Kind: ledger.Shared, Owner: a.ID, First: v},
		"private without owner": {Name: "x", Kind: ledger.Private, First: pv},
		"private with shares":   {Name: "x", Kind: ledger.Private, Owner: a.ID, First: v},
		"unknown owner":         {Name: "x", Kind: ledger.Private, Owner: 99, First: pv},
		"unknown payer":         {Name: "x", Kind: ledger.Shared, First: withPayer},
		"no due months":         {Name: "x", Kind: ledger.Shared, First: noDue},
		"empty name":            {Name: "  ", Kind: ledger.Shared, First: v},
		"unknown kind":          {Name: "x", Kind: "felles", First: v},
	}
	for name, e := range cases {
		if _, err := s.CreateExpense(ctx, e, a.ID); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.CreateExpense(ctx, NewExpense{Name: "x", Kind: ledger.Shared, First: v}, 99); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown creator: err = %v, want ErrInvalid", err)
	}
	if es, _ := s.Expenses(ctx); len(es) != 0 {
		t.Errorf("rejected expenses were stored: %+v", es)
	}
	if _, err := s.CreateExpense(ctx, NewExpense{Name: "Sparing", Kind: ledger.Private, Owner: b.ID, First: pv}, b.ID); err != nil {
		t.Errorf("valid private expense: %v", err)
	}
}

func TestRenameAndEnd(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	v := func(from string) ledger.ExpenseVersion {
		return ledger.ExpenseVersion{ValidFrom: month.MustParse(from), Amount: money.Kr(400), Due: month.Monthly, Shares: shares(a, b, 5000)}
	}
	id, err := s.CreateExpense(ctx, NewExpense{Name: "Trening", Kind: ledger.Shared, First: v("2026-01")}, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExpenseVersion(ctx, id, v("2026-05"), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameExpense(ctx, id, "Treningssenter", "Fritid"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExpenseEnd(ctx, id, month.MustParse("2026-04")); !errors.Is(err, ErrInvalid) {
		t.Errorf("end before latest version: err = %v, want ErrInvalid", err)
	}
	if err := s.SetExpenseEnd(ctx, id, month.MustParse("2026-08")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExpenseVersion(ctx, id, v("2026-09"), a.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("version after end: err = %v, want ErrInvalid", err)
	}
	e, _ := s.Expense(ctx, id)
	if e.Name != "Treningssenter" || e.Category != "Fritid" || e.End != month.MustParse("2026-08") {
		t.Errorf("expense = %+v", e)
	}
	if err := s.SetExpenseEnd(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Expense(ctx, id); !e.End.IsZero() {
		t.Errorf("end not cleared: %v", e.End)
	}
	if err := s.RenameExpense(ctx, 999, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename unknown: err = %v", err)
	}
	if err := s.SetExpenseEnd(ctx, 999, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("end unknown: err = %v", err)
	}
}

// The brief's required case, stored and loaded back through SQLite.
func TestLedgerRoundTrip(t *testing.T) {
	s := newStore(t)
	a, b := twoUsers(t, s)
	jan := month.MustParse("2026-01")
	for _, e := range []struct {
		name  string
		kr    int64
		payer ledger.UserID
	}{{"Husleie", 14_000, 0}, {"Mat", 4_000, 0}, {"Strøm", 2_000, a.ID}} {
		if _, err := s.CreateExpense(ctx, NewExpense{Name: e.name, Kind: ledger.Shared, First: ledger.ExpenseVersion{
			ValidFrom: jan, Amount: money.Kr(e.kr), Due: month.Monthly, Payer: e.payer, Shares: shares(a, b, 5000)}}, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	in, err := s.LedgerInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.Users, []ledger.UserID{a.ID, b.ID}) {
		t.Errorf("users = %v", in.Users)
	}
	st, err := ledger.Settle(in, month.MustParse("2026-11"))
	if err != nil {
		t.Fatal(err)
	}
	ra, _ := st.User(a.ID)
	rb, _ := st.User(b.ID)
	if ra.Transfer != money.Kr(8_000) || rb.Transfer != money.Kr(10_000) || st.Felleskonto != money.Kr(18_000) {
		t.Errorf("transfers %v / %v, felleskonto %v; want 8 000 / 10 000 / 18 000", ra.Transfer, rb.Transfer, st.Felleskonto)
	}
}
