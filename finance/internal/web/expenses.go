package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web/components"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
)

// ---- list ----

func (s *Server) expenses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := s.monthParam(r)
	users, err := s.store.Users(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	exps, err := s.store.Expenses(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	names := userNames(users)
	v := pages.ExpensesView{Nav: components.MonthNav{Path: "/utgifter", Month: m, Current: s.thisMonth()}, Empty: len(exps) == 0}

	// Felles first, then the logged-in person's own expenses ("Dine utgifter"), then
	// the other person's ("Rikkes utgifter"), which starts collapsed.
	me := currentUser(r).ID
	shared := &pages.ExpenseGroup{Title: "Felles", Open: true}
	private := map[ledger.UserID]*pages.ExpenseGroup{}
	order := []*pages.ExpenseGroup{shared}
	for _, u := range users {
		if u.ID == me {
			private[u.ID] = &pages.ExpenseGroup{Title: "Dine utgifter", Open: true}
			order = append(order, private[u.ID])
		}
	}
	for _, u := range users {
		if u.ID != me {
			private[u.ID] = &pages.ExpenseGroup{Title: components.Genitive(u.DisplayName) + " utgifter"}
			order = append(order, private[u.ID])
		}
	}
	for _, e := range exps {
		ver, active := e.At(m)
		if !active {
			latest := e.Versions[len(e.Versions)-1]
			row := expenseRow(e.Expense, latest, names, users)
			if m < e.Versions[0].ValidFrom {
				row.Status = "Starter " + e.Versions[0].ValidFrom.Label()
			} else {
				row.Status = "Avsluttet etter " + e.End.Label()
			}
			v.Inactive = append(v.Inactive, row)
			continue
		}
		g := shared
		if e.Kind == ledger.Private {
			g = private[e.Owner]
		}
		row := expenseRow(e.Expense, ver, names, users)
		g.Rows = append(g.Rows, row)
		g.Monthly += row.Monthly
	}
	for _, g := range order {
		if len(g.Rows) == 0 {
			continue
		}
		sort.SliceStable(g.Rows, func(i, j int) bool {
			if g.Rows[i].Monthly != g.Rows[j].Monthly {
				return g.Rows[i].Monthly > g.Rows[j].Monthly
			}
			return g.Rows[i].Name < g.Rows[j].Name
		})
		v.Groups = append(v.Groups, *g)
	}
	render(w, r, http.StatusOK, pages.Expenses(v))
}

func expenseRow(e ledger.Expense, v ledger.ExpenseVersion, names map[ledger.UserID]string, users []store.User) pages.ExpenseRow {
	var parts []string
	if e.Category != "" {
		parts = append(parts, e.Category)
	}
	parts = append(parts, v.Due.Describe())
	if v.Due != month.Monthly {
		parts = append(parts, money.Format(v.Amount)+" per gang")
	}
	if e.Kind == ledger.Shared {
		parts = append(parts, splitLabel(v.Shares, users))
		if v.Payer != ledger.Felleskonto {
			parts = append(parts, names[v.Payer]+" betaler")
		}
	}
	return pages.ExpenseRow{ID: e.ID, Icon: components.CategoryIcon(e.Category, e.Name), Name: e.Name,
		Details: strings.Join(parts, " · "), Monthly: ledger.MonthlyEquivalent(v.Amount, v.Due)}
}

// splitLabel describes a split: "delt likt", or "Haakon 60 %, Rikke 40 %".
func splitLabel(shares []ledger.Share, users []store.User) string {
	bp := map[ledger.UserID]int{}
	for _, sh := range shares {
		bp[sh.User] = sh.BP
	}
	if len(users) == 2 && bp[users[0].ID] == 5000 && bp[users[1].ID] == 5000 {
		return "delt likt"
	}
	var parts []string
	for _, u := range users {
		parts = append(parts, u.DisplayName+" "+formatPercent(bp[u.ID])+" %")
	}
	return strings.Join(parts, ", ")
}

// ---- form state ----

// expenseState is the form's content as typed, plus validation errors.
type expenseState struct {
	name, category, amount, share string
	kind                          ledger.Kind
	owner, payer                  ledger.UserID
	// freq is the frequency choice: monthly, semiannual, yearly, eleven or custom.
	// yearAnchor and semiAnchor (January–June) say which months for the yearly and
	// half-yearly choices; due is the resulting mask (from the grid when custom).
	freq                   string
	yearAnchor, semiAnchor time.Month
	due                    month.Mask
	validFrom              month.Month
	errors                 map[string]string
}

func defaultExpenseState(now month.Month, me ledger.UserID) expenseState {
	st := expenseState{kind: ledger.Shared, owner: me, share: "50", validFrom: now, errors: map[string]string{}}
	st.setFrequency(month.Monthly, now.Month())
	return st
}

// setFrequency picks the frequency choice that describes mask, so an edit form
// opens on the right option; anything irregular is "custom" with the grid ticked.
func (st *expenseState) setFrequency(mask month.Mask, fallback time.Month) {
	st.due, st.yearAnchor, st.semiAnchor = mask, fallback, (fallback-1)%6+1
	ms := mask.Months()
	switch {
	case mask == month.Monthly:
		st.freq = "monthly"
	case mask == month.ElevenMonths(time.July):
		st.freq = "eleven"
	case len(ms) == 1:
		st.freq, st.yearAnchor = "yearly", ms[0]
	case len(ms) == 2 && ms[1]-ms[0] == 6:
		st.freq, st.semiAnchor = "semiannual", ms[0]
	default:
		st.freq = "custom"
	}
}

// stateFromExpense pre-fills the edit form with the terms in effect this month (or
// the latest terms, if the expense has ended or not started), asking for changes
// from this month.
func stateFromExpense(e store.Expense, now month.Month, users []store.User) expenseState {
	v, ok := e.At(now)
	if !ok {
		v = e.Versions[len(e.Versions)-1]
	}
	from := max(now, e.Versions[0].ValidFrom)
	if !e.End.IsZero() && from > e.End {
		from = e.End
	}
	st := expenseState{
		name: e.Name, category: e.Category, kind: e.Kind, owner: e.Owner, payer: v.Payer,
		amount: money.FormatNumber(v.Amount), share: "50", validFrom: from, errors: map[string]string{},
	}
	st.setFrequency(v.Due, from.Month())
	for _, sh := range v.Shares {
		if len(users) > 0 && sh.User == users[0].ID {
			st.share = formatPercent(sh.BP)
		}
	}
	return st
}

// readExpenseForm overlays the posted values. Kind and owner only come from the
// form for new expenses.
func readExpenseForm(r *http.Request, st *expenseState, editing bool) {
	r.ParseForm()
	st.name = r.PostFormValue("name")
	st.category = r.PostFormValue("category")
	st.amount = r.PostFormValue("amount")
	st.share = r.PostFormValue("share")
	if !editing {
		st.kind = ledger.Kind(r.PostFormValue("kind"))
		st.owner = parseUserID(r.PostFormValue("owner"))
	}
	st.payer = parseUserID(r.PostFormValue("payer"))
	st.validFrom, _ = month.Parse(r.PostFormValue("valid_from"))
	if n, err := strconv.Atoi(r.PostFormValue("year_anchor")); err == nil && n >= 1 && n <= 12 {
		st.yearAnchor = time.Month(n)
	}
	if n, err := strconv.Atoi(r.PostFormValue("semi_anchor")); err == nil && n >= 1 && n <= 6 {
		st.semiAnchor = time.Month(n)
	}
	var grid month.Mask
	for _, raw := range r.PostForm["month"] {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 12 {
			grid |= month.MaskOf(time.Month(n))
		}
	}
	st.freq = r.PostFormValue("freq")
	switch st.freq {
	case "monthly":
		st.due = month.Monthly
	case "semiannual":
		st.due = month.Semiannual(st.semiAnchor)
	case "yearly":
		st.due = month.Yearly(st.yearAnchor)
	case "eleven":
		st.due = month.ElevenMonths(time.July)
	default: // custom (or no choice sent): the ticked months
		st.freq, st.due = "custom", grid
	}
}

func parseUserID(raw string) ledger.UserID {
	id, _ := strconv.ParseInt(raw, 10, 64)
	return ledger.UserID(id)
}

// validateExpense checks the form and builds the version to save.
func validateExpense(st *expenseState, users []store.User, existing *store.Expense) (ledger.ExpenseVersion, bool) {
	e := st.errors
	if strings.TrimSpace(st.name) == "" {
		e["name"] = "Gi utgiften et navn."
	} else if len([]rune(strings.TrimSpace(st.name))) > 100 {
		e["name"] = "Navnet kan ha maks 100 tegn."
	}
	if len([]rune(strings.TrimSpace(st.category))) > 50 {
		e["category"] = "Kategorien kan ha maks 50 tegn."
	}
	amount, msg := parseAmount(st.amount)
	if msg != "" {
		e["amount"] = msg
	}
	if st.due == 0 {
		e["due"] = "Velg minst én måned."
	}
	if st.validFrom.IsZero() {
		e["valid_from"] = "Velg måned."
	} else if existing != nil && !existing.End.IsZero() && st.validFrom > existing.End {
		e["valid_from"] = fmt.Sprintf("Utgiften er avsluttet etter %s. Velg en måned til og med den, eller gjenoppta utgiften.", existing.End.Label())
	}
	ids := make([]ledger.UserID, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	v := ledger.ExpenseVersion{ValidFrom: st.validFrom, Amount: amount, Due: st.due}
	switch st.kind {
	case ledger.Private:
		if !slices.Contains(ids, st.owner) {
			e["owner"] = "Velg hvem utgiften tilhører."
		}
	case ledger.Shared:
		bpA, msg := parsePercent(st.share)
		if msg != "" {
			e["share"] = msg
		}
		if len(users) == 1 {
			bpA = 10000
		}
		for i, u := range users {
			bp := bpA
			if i > 0 {
				bp = 10000 - bpA
			}
			v.Shares = append(v.Shares, ledger.Share{User: u.ID, BP: bp})
		}
		if st.payer != ledger.Felleskonto && !slices.Contains(ids, st.payer) {
			e["payer"] = "Velg hvem som betaler."
		}
		v.Payer = st.payer
	default:
		e["kind"] = "Velg felles eller privat."
	}
	return v, len(e) == 0
}

func sameTerms(a, b ledger.ExpenseVersion) bool {
	if a.Amount != b.Amount || a.Due != b.Due || a.Payer != b.Payer || len(a.Shares) != len(b.Shares) {
		return false
	}
	bp := map[ledger.UserID]int{}
	for _, sh := range a.Shares {
		bp[sh.User] = sh.BP
	}
	for _, sh := range b.Shares {
		if got, ok := bp[sh.User]; !ok || got != sh.BP {
			return false
		}
	}
	return true
}

// ---- form view ----

func (s *Server) expenseFormView(st expenseState, existing *store.Expense, users []store.User, exps []store.Expense) pages.ExpenseForm {
	now := s.thisMonth()
	names := userNames(users)
	f := pages.ExpenseForm{
		Action: "/utgifter", Name: st.name, Category: st.category, Kind: st.kind,
		Amount: st.amount, ShareA: st.share, Errors: st.errors, Focus: pages.FirstError(st.errors),
		ValidFrom: monthOptions(st.validFrom, now),
	}
	seen := map[string]bool{}
	for _, e := range exps {
		if e.Category != "" && !seen[e.Category] {
			seen[e.Category] = true
			f.Categories = append(f.Categories, e.Category)
		}
	}
	sort.Strings(f.Categories)
	for _, u := range users {
		f.Owners = append(f.Owners, pages.Option{Value: strconv.FormatInt(int64(u.ID), 10), Label: u.DisplayName, Selected: u.ID == st.owner})
	}
	f.Payers = append(f.Payers, pages.Option{Value: "0", Label: "Felleskontoen", Selected: st.payer == ledger.Felleskonto})
	for _, u := range users {
		f.Payers = append(f.Payers, pages.Option{Value: strconv.FormatInt(int64(u.ID), 10), Label: u.DisplayName + " (direkte)", Selected: u.ID == st.payer})
	}
	f.Freq = st.freq
	for m := time.January; m <= time.December; m++ {
		f.Months = append(f.Months, pages.MonthBox{Value: int(m), Label: month.ShortName(m), Checked: st.due.Has(m)})
		f.YearMonths = append(f.YearMonths, pages.Option{Value: strconv.Itoa(int(m)), Label: month.Name(m), Selected: m == st.yearAnchor})
		if m <= time.June {
			f.SemiMonths = append(f.SemiMonths, pages.Option{Value: strconv.Itoa(int(m)),
				Label: month.Name(m) + " og " + month.Name(m+6), Selected: m == st.semiAnchor})
		}
	}
	if len(users) > 0 {
		f.UserA = users[0].DisplayName
	}
	if len(users) > 1 {
		f.UserB = users[1].DisplayName
		if bp, msg := parsePercent(st.share); msg == "" {
			f.ShareBHint = f.UserB + " dekker " + formatPercent(10000-bp) + " %."
		} else {
			f.ShareBHint = msg
		}
	}
	if existing != nil {
		f.ID = existing.ID
		f.Action = fmt.Sprintf("/utgifter/%d", existing.ID)
		f.KindFixed = true
		f.KindLabel = "Felles"
		if existing.Kind == ledger.Private {
			f.KindLabel = "Privat (" + names[existing.Owner] + ")"
		}
		if !existing.End.IsZero() {
			f.Ended = existing.End.Label()
		}
		last := existing.Versions[len(existing.Versions)-1].ValidFrom
		endDefault := max(now, last)
		for m := last; m <= max(now, last).Add(24); m++ {
			f.EndMonths = append(f.EndMonths, pages.Option{Value: m.String(), Label: m.Label(), Selected: m == endDefault})
		}
		for i := len(existing.Versions) - 1; i >= 0; i-- {
			v, meta := existing.Versions[i], existing.VersionMeta[i]
			row := expenseRow(existing.Expense, v, names, users)
			f.History = append(f.History, fmt.Sprintf("Fra %s: %s · %s · registrert av %s %s",
				v.ValidFrom.Label(), money.Format(v.Amount), row.Details, names[meta.CreatedBy], components.Date(meta.CreatedAt)))
		}
	}
	return f
}

// ---- handlers ----

func (s *Server) expenseFormData(r *http.Request) ([]store.User, []store.Expense, error) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		return nil, nil, err
	}
	exps, err := s.store.Expenses(r.Context())
	return users, exps, err
}

func (s *Server) newExpense(w http.ResponseWriter, r *http.Request) {
	users, exps, err := s.expenseFormData(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st := defaultExpenseState(s.thisMonth(), currentUser(r).ID)
	render(w, r, http.StatusOK, pages.ExpenseFormPage(s.expenseFormView(st, nil, users, exps)))
}

func (s *Server) createExpense(w http.ResponseWriter, r *http.Request) {
	users, exps, err := s.expenseFormData(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st := defaultExpenseState(s.thisMonth(), currentUser(r).ID)
	readExpenseForm(r, &st, false)
	if s.formOnlyRequest(w, r, &st, nil, users, exps) {
		return
	}
	v, ok := validateExpense(&st, users, nil)
	if ok {
		_, err = s.store.CreateExpense(r.Context(), store.NewExpense{
			Name: st.name, Category: st.category, Kind: st.kind, Owner: ownerFor(st), First: v,
		}, currentUser(r).ID)
		if err == nil {
			redirectAfterPost(w, r, s.expensesURL(v.ValidFrom))
			return
		}
		if !errors.Is(err, store.ErrInvalid) {
			serverError(w, r, err)
			return
		}
		st.errors["form"] = "Utgiften kunne ikke lagres. Sjekk feltene og prøv igjen."
	}
	render(w, r, http.StatusUnprocessableEntity, pages.ExpenseFormPage(s.expenseFormView(st, nil, users, exps)))
}

func ownerFor(st expenseState) ledger.UserID {
	if st.kind == ledger.Private {
		return st.owner
	}
	return 0
}

// formOnlyRequest answers the live split preview, which re-renders the form without
// saving. It reports whether it responded.
func (s *Server) formOnlyRequest(w http.ResponseWriter, r *http.Request, st *expenseState, existing *store.Expense, users []store.User, exps []store.Expense) bool {
	if r.PostFormValue("preview") == "" {
		return false
	}
	render(w, r, http.StatusOK, pages.ExpenseFormPage(s.expenseFormView(*st, existing, users, exps)))
	return true
}

func (s *Server) expensesURL(m month.Month) string {
	if m == s.thisMonth() {
		return "/utgifter"
	}
	return "/utgifter?m=" + m.String()
}

func (s *Server) loadExpense(w http.ResponseWriter, r *http.Request) (store.Expense, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return store.Expense{}, false
	}
	e, err := s.store.Expense(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return store.Expense{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.Expense{}, false
	}
	return e, true
}

func (s *Server) editExpense(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadExpense(w, r)
	if !ok {
		return
	}
	users, exps, err := s.expenseFormData(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st := stateFromExpense(e, s.thisMonth(), users)
	render(w, r, http.StatusOK, pages.ExpenseFormPage(s.expenseFormView(st, &e, users, exps)))
}

// updateExpense saves an edit. Name and category are labels and change in place;
// the terms (amount, months, split, payer) get a new version from the chosen month,
// unless they are unchanged.
func (s *Server) updateExpense(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadExpense(w, r)
	if !ok {
		return
	}
	users, exps, err := s.expenseFormData(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st := stateFromExpense(e, s.thisMonth(), users)
	readExpenseForm(r, &st, true)
	if s.formOnlyRequest(w, r, &st, &e, users, exps) {
		return
	}
	v, ok := validateExpense(&st, users, &e)
	if ok {
		ctx, by := r.Context(), currentUser(r).ID
		name, category := strings.TrimSpace(st.name), strings.TrimSpace(st.category)
		if name != e.Name || category != e.Category {
			err = s.store.RenameExpense(ctx, e.ID, name, category)
		}
		if current, has := e.At(v.ValidFrom); err == nil && (!has || !sameTerms(current, v)) {
			err = s.store.SaveExpenseVersion(ctx, e.ID, v, by)
		}
		if err == nil {
			redirectAfterPost(w, r, s.expensesURL(v.ValidFrom))
			return
		}
		if !errors.Is(err, store.ErrInvalid) {
			serverError(w, r, err)
			return
		}
		st.errors["form"] = "Endringen kunne ikke lagres. Sjekk feltene og prøv igjen."
	}
	render(w, r, http.StatusUnprocessableEntity, pages.ExpenseFormPage(s.expenseFormView(st, &e, users, exps)))
}

// endExpense sets (or, with action=resume, clears) the last month of an expense.
func (s *Server) endExpense(w http.ResponseWriter, r *http.Request) {
	e, ok := s.loadExpense(w, r)
	if !ok {
		return
	}
	var end month.Month
	if r.PostFormValue("action") != "resume" {
		var err error
		if end, err = month.Parse(r.PostFormValue("end")); err != nil {
			s.renderEditWithError(w, r, e, "end", "Velg siste måned.")
			return
		}
	}
	err := s.store.SetExpenseEnd(r.Context(), e.ID, end)
	if errors.Is(err, store.ErrInvalid) {
		s.renderEditWithError(w, r, e, "end", "Siste måned kan ikke være før den siste endringen ("+e.Versions[len(e.Versions)-1].ValidFrom.Label()+").")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	redirectAfterPost(w, r, "/utgifter")
}

func (s *Server) renderEditWithError(w http.ResponseWriter, r *http.Request, e store.Expense, field, msg string) {
	users, exps, err := s.expenseFormData(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st := stateFromExpense(e, s.thisMonth(), users)
	st.errors[field] = msg
	render(w, r, http.StatusUnprocessableEntity, pages.ExpenseFormPage(s.expenseFormView(st, &e, users, exps)))
}
