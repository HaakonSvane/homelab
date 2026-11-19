package store

import (
	"context"
	"fmt"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// Seed fills an empty database with a fictional household (Kari and Ola Nordmann)
// for development, with versions starting in January of year. It refuses to touch
// a database that already has users, so it can never mix into real data.
func (s *Store) Seed(ctx context.Context, year int) error {
	existing, err := s.Users(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return fmt.Errorf("seed: database already has %d user(s); seed only an empty database", len(existing))
	}
	kari, err := s.CreateUser(ctx, "kari", "Kari")
	if err != nil {
		return err
	}
	ola, err := s.CreateUser(ctx, "ola", "Ola")
	if err != nil {
		return err
	}
	from := func(m time.Month) month.Month { return month.New(year, m) }

	for _, inc := range []struct {
		user   User
		from   time.Month
		amount money.Ore
		note   string
	}{
		{kari, time.January, money.Kr(42_000), ""},
		{kari, time.July, money.Kr(44_500), "Lønnsoppgjør"},
		{ola, time.January, money.Kr(47_000), ""},
	} {
		v := ledger.IncomeVersion{ValidFrom: from(inc.from), Amount: inc.amount}
		if err := s.SetIncome(ctx, inc.user.ID, v, inc.note, kari.ID); err != nil {
			return err
		}
	}

	half := []ledger.Share{{User: kari.ID, BP: 5000}, {User: ola.ID, BP: 5000}}
	type ver struct {
		from   time.Month
		amount money.Ore
	}
	expenses := []struct {
		name, category string
		kind           ledger.Kind
		owner          ledger.UserID
		due            month.Mask
		payer          ledger.UserID
		shares         []ledger.Share
		versions       []ver
		end            time.Month
	}{
		{"Boliglån", "Bolig", ledger.Shared, 0, month.Monthly, 0, half, []ver{{time.January, money.Kr(13_500)}}, 0},
		{"Felleskostnader", "Bolig", ledger.Shared, 0, month.Monthly, 0, half, []ver{{time.January, money.Kr(3_200)}}, 0},
		{"Mat og husholdning", "Mat", ledger.Shared, 0, month.Monthly, 0, half, []ver{{time.January, money.Kr(9_000)}, {time.September, money.Kr(9_500)}}, 0},
		{"Strøm", "Bolig", ledger.Shared, 0, month.Monthly, ola.ID, half, []ver{{time.January, money.Kr(1_600)}}, 0},
		{"Internett", "Abonnement", ledger.Shared, 0, month.Monthly, 0, half, []ver{{time.January, money.Kr(549)}}, 0},
		{"Strømmetjenester", "Abonnement", ledger.Shared, 0, month.Monthly, kari.ID, half, []ver{{time.January, money.Kr(389)}}, 0},
		{"Barnehage", "Barn", ledger.Shared, 0, month.ElevenMonths(time.July), 0, half, []ver{{time.January, money.Kr(1_500)}}, 0},
		{"Kommunale avgifter", "Bolig", ledger.Shared, 0, month.Semiannual(time.March), 0, half, []ver{{time.January, money.Kr(4_800)}}, 0},
		{"Bilforsikring", "Bil", ledger.Shared, 0, month.Yearly(time.April), 0,
			[]ledger.Share{{User: kari.ID, BP: 4000}, {User: ola.ID, BP: 6000}}, []ver{{time.January, money.Kr(9_000)}}, 0},
		{"Innboforsikring", "Forsikring", ledger.Shared, 0, month.Yearly(time.October), 0, half, []ver{{time.January, money.Kr(3_600)}}, 0},
		{"Sparing", "Sparing", ledger.Private, kari.ID, month.Monthly, 0, nil, []ver{{time.January, money.Kr(4_000)}}, 0},
		{"Mobilabonnement", "Abonnement", ledger.Private, kari.ID, month.Monthly, 0, nil, []ver{{time.January, money.Kr(399)}}, 0},
		{"Sparing", "Sparing", ledger.Private, ola.ID, month.Monthly, 0, nil, []ver{{time.January, money.Kr(4_000)}}, 0},
		{"Treningssenter", "Fritid", ledger.Private, ola.ID, month.Monthly, 0, nil, []ver{{time.January, money.Kr(549)}}, time.August},
	}
	for _, e := range expenses {
		version := func(v ver) ledger.ExpenseVersion {
			return ledger.ExpenseVersion{ValidFrom: from(v.from), Amount: v.amount, Due: e.due, Payer: e.payer, Shares: e.shares}
		}
		id, err := s.CreateExpense(ctx, NewExpense{Name: e.name, Category: e.category, Kind: e.kind, Owner: e.owner,
			First: version(e.versions[0])}, kari.ID)
		if err != nil {
			return fmt.Errorf("seed %s: %w", e.name, err)
		}
		for _, v := range e.versions[1:] {
			if err := s.SaveExpenseVersion(ctx, id, version(v), ola.ID); err != nil {
				return fmt.Errorf("seed %s: %w", e.name, err)
			}
		}
		if e.end != 0 {
			if err := s.SetExpenseEnd(ctx, id, from(e.end)); err != nil {
				return fmt.Errorf("seed %s: %w", e.name, err)
			}
		}
	}
	return nil
}
