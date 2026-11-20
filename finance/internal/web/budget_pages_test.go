package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
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
// money for November is 24 194,50 kr, so an even pace leaves 24 194,50 × 21/29 =
// 17 520,16 kr today, and 834,29 kr per day is normal.
func TestFreeSpending(t *testing.T) {
	h := newSeededHarness(t)
	mustContain(t, h.get("/", http.StatusOK), "Legg inn lønningsdagen din under") // no payday yet
	setKariPayday(t, h, "12")
	body := h.get("/", http.StatusOK)
	mustContain(t, body,
		"Til fri bruk", nb("17 520,16 kr"), `<progress max="29" value="8"`,
		"Lønning fredag 11. desember, om 21 dager", nb("834,29 kr"),
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
		{"tight", "1 000", "12 000", []string{"Ja, men det blir stramt.", nb("523,81 kr") + " per dag, mot vanlig " + nb("834,29 kr")}},
		{"rather not", "1 000", "8 000", []string{"Helst ikke nå.", nb("333,33 kr") + " per dag"}},
		{"not covered", "800", "500", []string{"Nei, saldoen dekker ikke kjøpet.", "Du mangler " + nb("300,00 kr")}},
		{"bigger than a month", "30 000", "60 000", []string{"Ja, det går fint.", "større enn en hel måneds fri bruk (" + nb("24 194,50 kr") + ")"}},
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
	// From November 2026 (see ledger.TestBufferNeed): 3 450 kr, bottoming out in
	// April 2027 when the car insurance is due. No irregular bills Dec–Feb.
	body := h.get("/", http.StatusOK)
	mustContain(t, body, "Buffer på felleskontoen", "Ved inngangen til november 2026", nb("3 450,00 kr"),
		"Ellers går den i minus i april 2027, når Bilforsikring skal betales.", `action="/felleskonto/saldo"`)
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
