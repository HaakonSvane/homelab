package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// setKariPayday re-saves Kari's July 2026 income (44 500 kr, the version in force in
// November) with a payday.
func setKariPayday(t *testing.T, h *harness, day string) {
	t.Helper()
	path := "/inntekt/" + strconv.FormatInt(int64(h.kari.ID), 10)
	status, body, _ := h.do("POST", path, form("amount", "44 500", "valid_from", "2026-07", "payday", day))
	if status != http.StatusSeeOther {
		t.Fatalf("set payday: %d\n%s", status, body)
	}
}

func TestPaydayOnIncome(t *testing.T) {
	h := newSeededHarness(t)
	setKariPayday(t, h, "12")
	// 12 December 2026 is a Saturday, so the next salary comes on Friday the 11th.
	mustContain(t, h.get("/inntekt", http.StatusOK), "Lønning den 12. · neste fredag 11. desember")
	mustContain(t, h.get("/inntekt/"+strconv.FormatInt(int64(h.kari.ID), 10)+"/endre", http.StatusOK),
		`<option value="12" selected>den 12.</option>`, "Lønningsdag")
	incs, _ := h.st.Incomes(context.Background())
	for _, inc := range incs {
		if inc.User == h.kari.ID && inc.ValidFrom.String() == "2026-07" && inc.Payday != 12 {
			t.Errorf("stored payday = %d", inc.Payday)
		}
	}
}

// With the clock at Friday 20 November 2026 and Kari's payday on the 12th, her pay
// period runs 12 November → 11 December: 29 days, 21 left including today. Her free
// money for November is 24 119,50 kr, so an even pace leaves 24 119,50 × 21/29 =
// 17 465,84 kr today, and 831,71 kr per day is normal.
func TestFreeSpending(t *testing.T) {
	h := newSeededHarness(t)
	mustContain(t, h.get("/", http.StatusOK), "Legg inn lønningsdagen din under") // no payday yet
	setKariPayday(t, h, "12")
	body := h.get("/", http.StatusOK)
	mustContain(t, body,
		"Til fri bruk", nb("17 465,84 kr"), `<progress max="29" value="8"`,
		"Lønning fredag 11. desember, om 21 dager", nb("831,71 kr"),
		"Legg inn lønningsdag under", // Ola has none
		`id="kan-jeg-kjope"`)
	// Other months don't show the "now" sections.
	if other := h.get("/?m=2026-10", http.StatusOK); strings.Contains(other, "Til fri bruk") || strings.Contains(other, "Kan jeg kjøpe?") {
		t.Error("now-sections shown for another month")
	}
}

func TestCanIBuy(t *testing.T) {
	h := newSeededHarness(t)
	setKariPayday(t, h, "12")
	before, _ := h.st.Incomes(context.Background())
	for _, c := range []struct {
		name, price, balance string
		want                 []string
	}{
		{"ahead of pace", "1 000", "20 000", []string{"Ja, det går fint.", nb("19 000,00 kr") + " til lønning om 21 dager"}},
		{"tight", "1 000", "12 000", []string{"Ja, men det blir stramt.", nb("523,81 kr") + " per dag, mot vanlig " + nb("831,71 kr")}},
		{"rather not", "1 000", "8 000", []string{"Helst ikke nå.", nb("333,33 kr") + " per dag"}},
		{"not covered", "800", "500", []string{"Nei, saldoen dekker ikke kjøpet.", "Du mangler " + nb("300,00 kr")}},
		{"bigger than a month", "30 000", "60 000", []string{"Ja, det går fint.", "større enn en hel måneds fri bruk (" + nb("24 119,50 kr") + ")"}},
	} {
		status, body, _ := h.do("POST", "/kan-jeg-kjope", form("price", c.price, "balance", c.balance), "HX-Request", "true")
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", c.name, status)
		}
		mustContain(t, body, c.want...)
	}
	status, body, _ := h.do("POST", "/kan-jeg-kjope", form("price", "", "balance", "abc"))
	if status != http.StatusUnprocessableEntity {
		t.Errorf("invalid input: status %d", status)
	}
	mustContain(t, body, "Fyll inn beløpet.", "Skriv beløpet som", `value="abc"`)
	if after, _ := h.st.Incomes(context.Background()); len(after) != len(before) {
		t.Error("the calculator changed stored data")
	}
}

func TestBufferAndUpcoming(t *testing.T) {
	h := newSeededHarness(t)
	// No paydays yet: the monthly estimate from November 2026 (see
	// ledger.TestBufferNeed), 3 450 kr. No irregular bills Nov–Feb.
	body := h.get("/", http.StatusOK)
	mustContain(t, body, "Buffer på felleskontoen",
		"Ved inngangen til november 2026 bør felleskontoen ha minst "+nb("3 450,00 kr")+" før månedens overføringer kommer inn.",
		"Et grovt anslag per måned. Legg inn lønningsdag for begge", `action="/felleskonto/saldo"`)
	if strings.Contains(body, "Kommende store regninger") {
		t.Error("upcoming-bills section shown although it is empty")
	}
	feb := h.get("/?m=2027-02", http.StatusOK)
	mustContain(t, feb, "Kommende store regninger", "Kommunale avgifter", "mars 2027 · Felleskonto", "Bilforsikring", "april 2027")
	if strings.Contains(feb, `action="/felleskonto/saldo"`) {
		t.Error("balance check offered for another month")
	}

	_, body, _ = h.do("POST", "/felleskonto/saldo", form("balance", "5 000"))
	mustContain(t, body, "Saldoen er "+nb("1 550,00 kr")+" over det bufferen bør være.")
	_, body, _ = h.do("POST", "/felleskonto/saldo", form("balance", "1 000"))
	mustContain(t, body, "Saldoen er "+nb("2 450,00 kr")+" under det bufferen bør være.")
	if status, _, _ := h.do("POST", "/felleskonto/saldo", form("balance", "-5")); status != http.StatusUnprocessableEntity {
		t.Errorf("negative balance: %d", status)
	}
}

// Regression: income and expenses registered from this month, with a payday later
// in the month. Before payday the period started on last month's salary, which has
// no figures; the free money must come from this month, not read as 0 kr.
func TestFreeSpendingBeforeFirstRegisteredPayday(t *testing.T) {
	h := newHarness(t)
	oslo, _ := time.LoadLocation("Europe/Oslo")
	time.Local = oslo
	h.st.Now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, oslo) }
	h.register(newSoftAuthenticator(t, testOrigin, testRPID), h.enrollToken("kari"))
	kari := "/inntekt/" + strconv.FormatInt(int64(h.kari.ID), 10)
	if status, _, _ := h.do("POST", kari, form("amount", "40 000", "valid_from", "2026-11", "payday", "26")); status != http.StatusSeeOther {
		t.Fatalf("income: %d", status)
	}
	if status, _, _ := h.do("POST", "/utgifter", form("name", "Husleie", "kind", "shared", "amount", "10 000", "freq", "monthly",
		"payer", "0", "valid_from", "2026-11")); status != http.StatusSeeOther {
		t.Fatalf("expense: %d", status)
	}
	// Period 26 Oct (Mon) → 26 Nov (Thu): 31 days, 6 left. November's free money is
	// 40 000 − 5 000 = 35 000 kr: 35 000 × 6/31 = 6 774,19 kr now, 1 129,03 kr a day.
	body := h.get("/", http.StatusOK)
	mustContain(t, body, nb("6 774,19 kr"), nb("1 129,03 kr"), "om 6 dager",
		"Regnet ut fra tallene for november, siden det ikke er registrert noe for oktober.")
	if strings.Contains(body, nb("0,00 kr")+"</span> per dag") {
		t.Error("still shows 0 kr per day")
	}
	_, body, _ = h.do("POST", "/kan-jeg-kjope", form("price", "500", "balance", "8 000"))
	mustContain(t, body, "Ja, det går fint.")
}

// With both paydays (Kari the 12th, Ola the 26th) the buffer follows the dates.
// Every seed bill counts as drawn on the 1st, before either transfer. Each month
// 15 592,50 + 14 381,50 = 29 974 kr comes in. From 21 November (after today's
// transactions): +14 381,50 on 26 Nov, −28 249 on 1 Dec, +15 592,50 on 11 Dec, …
// The deepest point is Thursday 1 April 2027, when Bilforsikring (9 000) comes on top
// of the regular bills: −20 767,50. From 1 March the requirement is 40 324 kr, which
// is 20 767,50 plus the 19 556,50 net that comes in from 21 November to the end of
// February.
func TestDatedBuffer(t *testing.T) {
	h := newSeededHarness(t)
	setKariPayday(t, h, "12")
	ola := "/inntekt/" + strconv.FormatInt(int64(h.ola.ID), 10)
	if status, _, _ := h.do("POST", ola, form("amount", "47 000", "valid_from", "2026-01", "payday", "26")); status != http.StatusSeeOther {
		t.Fatalf("Ola's payday: %d", status)
	}
	mustContain(t, h.get("/", http.StatusOK),
		"Felleskontoen bør ha minst "+nb("20 767,50 kr")+" nå.",
		"Regnet med datoene for overføringer og trekk de neste tolv månedene, etter dagens transaksjoner.",
		"Lavest torsdag 1. april 2027, når Bilforsikring trekkes.")
	mustContain(t, h.get("/?m=2027-03", http.StatusOK),
		"Ved inngangen til mars 2027 bør felleskontoen ha minst "+nb("40 324,00 kr")+".")

	_, body, _ := h.do("POST", "/felleskonto/saldo", form("balance", "25 000"))
	mustContain(t, body, "Saldoen er "+nb("4 232,50 kr")+" over det bufferen bør være.")
	_, body, _ = h.do("POST", "/felleskonto/saldo", form("balance", "1 000"))
	mustContain(t, body, "Saldoen er "+nb("19 767,50 kr")+" under det bufferen bør være.")
}
