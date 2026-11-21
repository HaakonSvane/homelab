package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
)

// newSeededHarness serves the fictional seed household (see store.Seed) with the
// clock fixed at 20 November 2026, logged in as Kari.
func newSeededHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	oslo, _ := time.LoadLocation("Europe/Oslo")
	time.Local = oslo
	st.Now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, oslo) }
	if err := st.Seed(ctx, 2026); err != nil {
		t.Fatal(err)
	}
	h := newHarnessFor(t, st)
	h.kari, _ = st.UserByName(ctx, "kari")
	h.ola, _ = st.UserByName(ctx, "ola")
	h.register(newSoftAuthenticator(t, testOrigin, testRPID), h.enrollToken("kari"))
	return h
}

// nb turns the plain spaces in a test string into the NBSPs nb-NO formatting uses.
func nb(s string) string { return strings.ReplaceAll(s, " ", " ") }

func (h *harness) get(path string, want int) string {
	h.t.Helper()
	status, body, _ := h.do("GET", path, "")
	if status != want {
		h.t.Fatalf("GET %s: %d, want %d\n%s", path, status, want, body)
	}
	return body
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func expenseByName(t *testing.T, st *store.Store, name string) store.Expense {
	t.Helper()
	es, err := st.Expenses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no expense %q", name)
	return store.Expense{}
}

func form(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(kv[i] + "=" + urlEscape(kv[i+1]))
	}
	return b.String()
}

func urlEscape(s string) string {
	r := strings.NewReplacer(" ", "+", ",", "%2C", "&", "%26", "=", "%3D")
	return r.Replace(s)
}

var allMonths = []string{"month", "1", "month", "2", "month", "3", "month", "4", "month", "5", "month", "6",
	"month", "7", "month", "8", "month", "9", "month", "10", "month", "11", "month", "12"}

func TestOverviewShowsSettlement(t *testing.T) {
	h := newSeededHarness(t)
	body := h.get("/", http.StatusOK) // November 2026 by the fixed clock
	mustContain(t, body,
		"God ettermiddag, Kari</h1>",
		"november 2026",
		"Til felleskontoen",
		nb("15 592,50"), nb("14 381,50"), nb("29 974,00"), // transfers and total (see store.TestSeedNovember)
		nb("24 119,50"), nb("27 018,50"), // leftovers
		"Ola betaler i tillegg <span class=\"num\">"+nb("1 600,00 kr")+"</span>",
	)
	// July: Kari's raise has applied.
	mustContain(t, h.get("/?m=2026-07", http.StatusOK), "Inntekt "+nb("44 500,00"))
	if strings.Contains(body, "Forfaller i") {
		t.Error(`the "Forfaller i" section is gone`)
	}
	if strings.Contains(body, "Mangler inntekt") {
		t.Error("missing-income notice shown although both have income")
	}
}

func TestExpensesList(t *testing.T) {
	h := newSeededHarness(t)
	body := h.get("/utgifter", http.StatusOK)
	mustContain(t, body,
		`<details class="group" open><summary><h2>Felles</h2>`,
		`<details class="group" open><summary><h2>Dine utgifter</h2>`, // Kari is logged in
		`<details class="group"><summary><h2>Olas utgifter</h2>`,      // the other person's start collapsed
		nb("31 963,00"), // shared monthly total
		"Halvårlig (mar og sep)", nb("4 800,00 kr")+" per gang",
		"Avsluttede og kommende", "Avsluttet etter august 2026", "Treningssenter",
		`<svg class="icon" viewBox="0 0 256 256"`, `<path d="M217.9,110.1l-80-80`, // Phosphor's house icon for Bolig
		`hx-get="/utgifter/ny"`, `popovertarget="sheet"`,
	)
	if strings.Contains(body, "delt likt") || strings.Contains(body, " %") {
		t.Error("expense rows still describe a split")
	}
}

func TestCreateExpense(t *testing.T) {
	h := newSeededHarness(t)
	before, _ := h.st.Expenses(context.Background())

	body := form(append([]string{"name", "Strømmeavtale", "category", "Bolig", "kind", "shared", "amount", "1 249,50",
		"payer", "0", "valid_from", "2026-11"}, allMonths...)...)
	status, _, hdr := h.do("POST", "/utgifter", body)
	if status != http.StatusSeeOther || hdr.Get("Location") != "/utgifter" {
		t.Fatalf("create: %d → %s", status, hdr.Get("Location"))
	}
	e := expenseByName(t, h.st, "Strømmeavtale")
	if v := e.Versions[0]; v.Amount != 124950 || v.Due != month.Monthly || v.Payer != 0 || len(v.Shares) != 2 || v.Shares[0].BP != 5000 {
		t.Errorf("stored version = %+v", v)
	}

	// Through htmx the response tells the page to reload instead of redirecting.
	body = form(append([]string{"name", "Sparing ferie", "kind", "private", "owner", strconv.FormatInt(int64(h.kari.ID), 10),
		"amount", "500", "valid_from", "2027-01"}, allMonths...)...)
	status, _, hdr = h.do("POST", "/utgifter", body, "HX-Request", "true")
	if status != http.StatusNoContent || hdr.Get("HX-Redirect") != "/utgifter?m=2027-01" {
		t.Fatalf("htmx create: %d, HX-Redirect %q", status, hdr.Get("HX-Redirect"))
	}
	if e := expenseByName(t, h.st, "Sparing ferie"); e.Owner != h.kari.ID || e.Versions[0].ValidFrom != month.MustParse("2027-01") {
		t.Errorf("private expense = %+v", e)
	}
	after, _ := h.st.Expenses(context.Background())
	if len(after) != len(before)+2 {
		t.Errorf("expenses %d → %d", len(before), len(after))
	}
}

func TestCreateExpenseValidation(t *testing.T) {
	h := newSeededHarness(t)
	before, _ := h.st.Expenses(context.Background())
	status, body, _ := h.do("POST", "/utgifter", form("name", "", "kind", "shared", "amount", "tusen", "payer", "0", "valid_from", "2026-11"))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", status)
	}
	mustContain(t, body, "Gi utgiften et navn.", "Skriv beløpet som", "Velg minst én måned.",
		`value="tusen"`) // typed input is kept
	if after, _ := h.st.Expenses(context.Background()); len(after) != len(before) {
		t.Error("invalid expense was stored")
	}
}

func TestFrequencyChoices(t *testing.T) {
	h := newSeededHarness(t)
	create := func(name string, kv ...string) month.Mask {
		t.Helper()
		body := form(append([]string{"name", name, "kind", "shared", "amount", "1000", "payer", "0", "valid_from", "2026-11"}, kv...)...)
		if status, b, _ := h.do("POST", "/utgifter", body); status != http.StatusSeeOther {
			t.Fatalf("%s: %d\n%s", name, status, b)
		}
		return expenseByName(t, h.st, name).Versions[0].Due
	}
	for _, c := range []struct {
		name string
		kv   []string
		want month.Mask
	}{
		{"Hver måned", []string{"freq", "monthly"}, month.Monthly},
		{"Halvårlig mars", []string{"freq", "semiannual", "semi_anchor", "3"}, month.MaskOf(3, 9)},
		{"Årlig oktober", []string{"freq", "yearly", "year_anchor", "10"}, month.MaskOf(10)},
		{"Barnehage 2", []string{"freq", "eleven"}, month.ElevenMonths(7)},
		{"Kvartalsvis", []string{"freq", "custom", "month", "1", "month", "4", "month", "7", "month", "10"}, month.MaskOf(1, 4, 7, 10)},
		// The grid is ignored unless "Egendefinert" is chosen.
		{"Årlig med rot i rutenettet", []string{"freq", "yearly", "year_anchor", "5", "month", "1", "month", "2"}, month.MaskOf(5)},
	} {
		if got := create(c.name, c.kv...); got != c.want {
			t.Errorf("%s: mask %012b, want %012b", c.name, got, c.want)
		}
	}
	if status, body, _ := h.do("POST", "/utgifter", form("name", "x", "kind", "shared", "amount", "1", "valid_from", "2026-11", "freq", "custom")); status != http.StatusUnprocessableEntity || !strings.Contains(body, "Velg minst én måned.") {
		t.Errorf("custom with no months: %d", status)
	}

	// Editing opens on the choice that matches the stored months.
	komm := expenseByName(t, h.st, "Kommunale avgifter") // half-yearly, March and September
	page := h.get("/utgifter/"+strconv.FormatInt(komm.ID, 10)+"/endre", http.StatusOK)
	mustContain(t, page, `value="semiannual" checked`, `<option value="3" selected>mars og september</option>`)
	kv := expenseByName(t, h.st, "Kvartalsvis")
	page = h.get("/utgifter/"+strconv.FormatInt(kv.ID, 10)+"/endre", http.StatusOK)
	mustContain(t, page, `value="custom" checked`, `value="4" checked`)
}

func TestEditExpenseVersionsAndLabels(t *testing.T) {
	h := newSeededHarness(t)
	strom := expenseByName(t, h.st, "Strøm")
	path := "/utgifter/" + strconv.FormatInt(strom.ID, 10)
	ola := strconv.FormatInt(int64(h.ola.ID), 10)

	page := h.get(path+"/endre", http.StatusOK)
	mustContain(t, page, "Fra hvilken måned?", "Historikk", nb(`value="1 600,00"`))

	post := func(name, amount, from string) {
		t.Helper()
		status, body, _ := h.do("POST", path, form(append([]string{"name", name, "category", "Bolig", "amount", amount,
			"payer", ola, "valid_from", from}, allMonths...)...))
		if status != http.StatusSeeOther {
			t.Fatalf("edit: %d\n%s", status, body)
		}
	}
	post("Strøm", "1 800", "2026-12")          // new price from December
	post("Strøm", "1 850", "2026-12")          // correct the December version
	post("Strøm (Tibber)", "1 600", "2026-11") // rename only: November terms unchanged

	e := expenseByName(t, h.st, "Strøm (Tibber)")
	if len(e.Versions) != 2 || e.Versions[0].Amount != money.Kr(1600) || e.Versions[1].Amount != money.Kr(1850) ||
		e.Versions[1].ValidFrom != month.MustParse("2026-12") {
		t.Errorf("versions = %+v", e.Versions)
	}
}

func TestEndAndResumeExpense(t *testing.T) {
	h := newSeededHarness(t)
	e := expenseByName(t, h.st, "Internett")
	path := "/utgifter/" + strconv.FormatInt(e.ID, 10) + "/avslutt"
	if status, _, _ := h.do("POST", path, form("end", "2026-12")); status != http.StatusSeeOther {
		t.Fatalf("end: %d", status)
	}
	mustContain(t, h.get("/utgifter?m=2027-01", http.StatusOK), "Avsluttet etter desember 2026")
	status, body, _ := h.do("POST", path, form("end", "2025-12"))
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "kan ikke være før den siste endringen") {
		t.Errorf("end before last version: %d", status)
	}
	if status, _, _ := h.do("POST", path, form("action", "resume")); status != http.StatusSeeOther {
		t.Fatalf("resume: %d", status)
	}
	if e := expenseByName(t, h.st, "Internett"); !e.End.IsZero() {
		t.Errorf("not resumed: end %v", e.End)
	}
	h.get("/utgifter/999999/endre", http.StatusNotFound)
}

func TestIncomePage(t *testing.T) {
	h := newSeededHarness(t)
	body := h.get("/inntekt", http.StatusOK)
	mustContain(t, body, nb("44 500,00 kr"), nb("47 000,00 kr"), "Fra juli 2026", "Lønnsoppgjør", "registrert av Kari")

	kari := "/inntekt/" + strconv.FormatInt(int64(h.kari.ID), 10)
	mustContain(t, h.get(kari+"/endre", http.StatusOK), `id="income-form"`, nb(`value="44 500,00"`))
	status, _, hdr := h.do("POST", kari, form("amount", "46 000", "valid_from", "2027-01", "note", "Ny jobb"))
	if status != http.StatusSeeOther || hdr.Get("Location") != "/inntekt?m=2027-01" {
		t.Fatalf("save income: %d → %s", status, hdr.Get("Location"))
	}
	mustContain(t, h.get("/inntekt?m=2027-01", http.StatusOK), nb("46 000,00 kr"), "Ny jobb")
	// November is unchanged: history is never rewritten.
	mustContain(t, h.get("/inntekt?m=2026-11", http.StatusOK), nb("44 500,00 kr"))

	status, body, _ = h.do("POST", kari, form("amount", "", "valid_from", "2027-01"))
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "Fyll inn beløpet.") {
		t.Errorf("empty amount: %d", status)
	}
	h.get("/inntekt/999/endre", http.StatusNotFound)
}

func TestStandaloneForms(t *testing.T) {
	h := newSeededHarness(t)
	page := h.get("/utgifter/ny", http.StatusOK)
	mustContain(t, page, `id="expense-form"`, "Gjelder fra", `name="kind"`, `name="payer"`)
	if strings.Contains(page, "Andel") || strings.Contains(page, `name="share"`) {
		t.Error("the split field is gone: shared expenses are always 50/50")
	}
}

func TestDeleteExpense(t *testing.T) {
	h := newSeededHarness(t)
	e := expenseByName(t, h.st, "Internett")
	id := strconv.FormatInt(e.ID, 10)

	// The edit sheet offers it behind the browser's confirm dialog.
	mustContain(t, h.get("/utgifter/"+id+"/endre", http.StatusOK),
		`hx-post="/utgifter/`+id+`/slett"`, `hx-confirm="Slette «Internett» for godt?`, "All historikk for utgiften slettes også", "Slett utgift")

	// Without confirmation (no JavaScript): a confirmation page, nothing deleted.
	status, body, _ := h.do("POST", "/utgifter/"+id+"/slett", "")
	if status != http.StatusOK {
		t.Fatalf("unconfirmed: %d", status)
	}
	mustContain(t, body, "Slette «Internett»?", "1 versjon", `name="bekreft" value="ja"`, "Slett for godt")
	if _, err := h.st.Expense(context.Background(), e.ID); err != nil {
		t.Fatalf("deleted without confirmation: %v", err)
	}

	// Confirmed: gone, and the settlement no longer counts it.
	status, _, hdr := h.do("POST", "/utgifter/"+id+"/slett", form("bekreft", "ja"))
	if status != http.StatusSeeOther || hdr.Get("Location") != "/utgifter" {
		t.Fatalf("confirmed: %d → %s", status, hdr.Get("Location"))
	}
	if _, err := h.st.Expense(context.Background(), e.ID); err == nil {
		t.Fatal("still there")
	}
	mustContain(t, h.get("/", http.StatusOK), nb("29 425,00")) // 29 974 − 549
	h.do("POST", "/utgifter/"+id+"/slett", form("bekreft", "ja"))
	if status, _, _ := h.do("POST", "/utgifter/"+id+"/slett", form("bekreft", "ja")); status != http.StatusNotFound {
		t.Errorf("deleting again: %d, want 404", status)
	}

	// Through htmx the page is told to reload.
	gym := expenseByName(t, h.st, "Treningssenter")
	status, _, hdr = h.do("POST", "/utgifter/"+strconv.FormatInt(gym.ID, 10)+"/slett", form("bekreft", "ja"), "HX-Request", "true")
	if status != http.StatusNoContent || hdr.Get("HX-Redirect") != "/utgifter" {
		t.Errorf("htmx delete: %d, HX-Redirect %q", status, hdr.Get("HX-Redirect"))
	}
}
