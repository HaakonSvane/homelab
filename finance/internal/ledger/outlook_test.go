package ledger

import (
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// The seed household's felleskonto bills (see store.Seed), from September 2026 on.
func seedLikeInput() Input {
	return Input{Users: users, Expenses: []Expense{
		sharedExp(1, "Boliglån", money.Kr(13_500), month.Monthly, Felleskonto, 5000),
		sharedExp(2, "Felleskostnader", money.Kr(3_200), month.Monthly, Felleskonto, 5000),
		sharedExp(3, "Mat", money.Kr(9_500), month.Monthly, Felleskonto, 5000),
		sharedExp(4, "Strøm", money.Kr(1_600), month.Monthly, B, 5000), // paid directly: not felleskonto
		sharedExp(5, "Internett", money.Kr(549), month.Monthly, Felleskonto, 5000),
		sharedExp(6, "Barnehage", money.Kr(1_500), month.ElevenMonths(time.July), Felleskonto, 5000),
		sharedExp(7, "Kommunale avgifter", money.Kr(4_800), month.Semiannual(time.March), Felleskonto, 5000),
		sharedExp(8, "Bilforsikring", money.Kr(9_000), month.Yearly(time.April), Felleskonto, 4000),
		sharedExp(9, "Innboforsikring", money.Kr(3_600), month.Yearly(time.October), Felleskonto, 5000),
	}}
}

// Worked by hand from November 2026: 29 974 kr comes in every month; regular bills
// take 26 749 (+1 500 kindergarten except July). Running total: Nov +1 725, Dec
// 3 450, Jan 5 175, Feb 6 900, Mar 3 825 (−4 800 kommunale), Apr −3 450 (−9 000 car
// insurance), May −1 725, Jun 0, Jul 3 225, Aug 4 950, Sep 1 875, Oct 0. Lowest is
// −3 450 in April, so the account needs 3 450 kr before November's transfers.
func TestBufferNeed(t *testing.T) {
	plan, err := BufferNeed(seedLikeInput(), month.MustParse("2026-11"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Required != money.Kr(3_450) || plan.LowMonth != month.MustParse("2027-04") || plan.LowBill.Name != "Bilforsikring" {
		t.Errorf("plan = %+v", plan)
	}

	// Only monthly bills: transfers always cover them, no buffer needed.
	regular := Input{Users: users, Expenses: []Expense{sharedExp(1, "Husleie", money.Kr(10_000), month.Monthly, Felleskonto, 5000)}}
	if plan, _ := BufferNeed(regular, month.MustParse("2026-11"), 12); plan.Required != 0 || !plan.LowMonth.IsZero() {
		t.Errorf("regular plan = %+v", plan)
	}
}

func TestUpcoming(t *testing.T) {
	in := seedLikeInput()
	if got, _ := Upcoming(in, month.MustParse("2026-11"), 3); len(got) != 0 {
		t.Errorf("Dec–Feb should have no irregular bills, got %+v", got)
	}
	got, err := Upcoming(in, month.MustParse("2027-02"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Kommunale avgifter" || got[0].Month != month.MustParse("2027-03") ||
		got[1].Name != "Bilforsikring" || got[1].Month != month.MustParse("2027-04") || got[1].Amount != money.Kr(9_000) {
		t.Errorf("Mar–May = %+v", got)
	}
	// Kindergarten (11 months a year) is not "irregular".
	for _, b := range got {
		if b.Name == "Barnehage" {
			t.Error("kindergarten listed as an irregular bill")
		}
	}
}
