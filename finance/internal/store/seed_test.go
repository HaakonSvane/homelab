package store

import (
	"testing"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

func ore(kr, ore int64) money.Ore { return money.Ore(kr*100 + ore) }

// The seed household, settled by hand for November 2026:
//
//	felleskonto: 13 500 + 3 200 + 9 500 (Mat, from Sep) + 549 + 1 375 (Barnehage 1 500×11/12)
//	             + 800 (Kommunale 4 800×2/12) + 750 (Bil 9 000/12) + 300 (Innbo 3 600/12) = 29 974
//	paid directly: Ola 1 600 (Strøm), Kari 389 (Strømmetjenester)
//	shared total 31 963; all 50/50 except Bil (Kari 40 %, Ola 60 %):
//	  Kari: (31 963 − 750)/2 + 300 = 15 906,50 → transfer 15 906,50 − 389   = 15 517,50
//	  Ola:  (31 963 − 750)/2 + 450 = 16 056,50 → transfer 16 056,50 − 1 600 = 14 456,50
//	private: Kari 4 000 + 399 = 4 399; Ola 4 000 (Treningssenter ended in August)
//	leftover: Kari 44 500 − 15 906,50 − 4 399 = 24 194,50; Ola 47 000 − 16 056,50 − 4 000 = 26 943,50
func TestSeedNovember(t *testing.T) {
	s := newStore(t)
	if err := s.Seed(ctx, 2026); err != nil {
		t.Fatal(err)
	}
	in, err := s.LedgerInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Settle(in, month.MustParse("2026-11"))
	if err != nil {
		t.Fatal(err)
	}
	kari, _ := s.UserByName(ctx, "kari")
	ola, _ := s.UserByName(ctx, "ola")
	k, _ := st.User(kari.ID)
	o, _ := st.User(ola.ID)

	if st.Felleskonto != money.Kr(29_974) {
		t.Errorf("felleskonto = %v", st.Felleskonto)
	}
	type figures struct{ income, obligation, paidDirect, transfer, private, leftover money.Ore }
	got := map[string]figures{
		"kari": {k.Income, k.Obligation, k.PaidDirect, k.Transfer, k.Private, k.Leftover},
		"ola":  {o.Income, o.Obligation, o.PaidDirect, o.Transfer, o.Private, o.Leftover},
	}
	want := map[string]figures{
		"kari": {money.Kr(44_500), ore(15_906, 50), money.Kr(389), ore(15_517, 50), money.Kr(4_399), ore(24_194, 50)},
		"ola":  {money.Kr(47_000), ore(16_056, 50), money.Kr(1_600), ore(14_456, 50), money.Kr(4_000), ore(26_943, 50)},
	}
	for name := range want {
		if got[name] != want[name] {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got[name], want[name])
		}
	}
	if len(st.Reimbursements) != 0 {
		t.Errorf("unexpected reimbursements %+v", st.Reimbursements)
	}
	if len(st.Due) != 10 || st.Due[0].Name != "Boliglån" {
		t.Errorf("due in November = %d items starting with %+v; want 10 starting with Boliglån", len(st.Due), st.Due[0])
	}
}

// Mid-year changes in the seed: Kari's raise from July, Mat from September, and
// Treningssenter ending after August.
func TestSeedOverTheYear(t *testing.T) {
	s := newStore(t)
	if err := s.Seed(ctx, 2026); err != nil {
		t.Fatal(err)
	}
	in, _ := s.LedgerInput(ctx)
	kari, _ := s.UserByName(ctx, "kari")
	ola, _ := s.UserByName(ctx, "ola")
	for _, c := range []struct {
		month                   string
		felleskonto, kariIncome money.Ore
		olaPrivate              money.Ore
	}{
		{"2026-06", money.Kr(29_474), money.Kr(42_000), money.Kr(4_549)},
		{"2026-07", money.Kr(29_474), money.Kr(44_500), money.Kr(4_549)},
		{"2026-08", money.Kr(29_474), money.Kr(44_500), money.Kr(4_549)},
		{"2026-09", money.Kr(29_974), money.Kr(44_500), money.Kr(4_000)},
	} {
		st, err := ledger.Settle(in, month.MustParse(c.month))
		if err != nil {
			t.Fatal(err)
		}
		k, _ := st.User(kari.ID)
		o, _ := st.User(ola.ID)
		if st.Felleskonto != c.felleskonto || k.Income != c.kariIncome || o.Private != c.olaPrivate {
			t.Errorf("%s: felleskonto %v, Kari income %v, Ola private %v; want %v, %v, %v",
				c.month, st.Felleskonto, k.Income, o.Private, c.felleskonto, c.kariIncome, c.olaPrivate)
		}
	}
}

func TestSeedRefusesNonEmptyDatabase(t *testing.T) {
	s := newStore(t)
	if _, err := s.CreateUser(ctx, "someone", "Someone"); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(ctx, 2026); err == nil {
		t.Fatal("Seed wrote into a database that already had users")
	}
	if us, _ := s.Users(ctx); len(us) != 1 {
		t.Errorf("users = %+v", us)
	}
}
