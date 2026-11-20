package web

import (
	"net/http"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/budget"
	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web/components"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
)

// How far ahead the outlook sections look.
const (
	bufferMonths   = 12
	upcomingMonths = 3
)

// overviewInput is what the two calculators on the overview were given. Nothing in
// it is stored or logged.
type overviewInput struct {
	buy         bool // "Kan jeg kjøpe?" was submitted
	price       string
	balance     string
	bufferCheck bool // "Sjekk saldo" was submitted
	fkBalance   string
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	s.renderOverview(w, r, s.monthParam(r), overviewInput{})
}

// canIBuy answers "Kan jeg kjøpe?". It is a calculation, not a change, so it renders
// the overview with the answer instead of redirecting; htmx swaps in just the answer.
func (s *Server) canIBuy(w http.ResponseWriter, r *http.Request) {
	s.renderOverview(w, r, s.thisMonth(), overviewInput{buy: true, price: r.PostFormValue("price"), balance: r.PostFormValue("balance")})
}

// checkBuffer compares a typed felleskonto balance with the required buffer.
func (s *Server) checkBuffer(w http.ResponseWriter, r *http.Request) {
	s.renderOverview(w, r, s.thisMonth(), overviewInput{bufferCheck: true, fkBalance: r.PostFormValue("balance")})
}

func (s *Server) renderOverview(w http.ResponseWriter, r *http.Request, m month.Month, input overviewInput) {
	ctx := r.Context()
	u := currentUser(r)
	users, err := s.store.Users(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	in, err := s.store.LedgerInput(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	st, err := ledger.Settle(in, m)
	if err != nil {
		serverError(w, r, err)
		return
	}
	names := userNames(users)

	v := pages.OverviewView{
		Greeting:    components.Greeting(s.store.Now().In(time.Local), u.DisplayName),
		Nav:         components.MonthNav{Path: "/", Month: m, Current: s.thisMonth()},
		Felleskonto: st.Felleskonto,
		NoExpenses:  len(in.Expenses) == 0,
		IsCurrent:   m == s.thisMonth(),
	}
	for _, ur := range st.Users {
		v.Users = append(v.Users, pages.OverviewUser{
			Name: names[ur.User], ToFelleskonto: ur.ToFelleskonto, PaidDirect: ur.PaidDirect,
			Income: ur.Income, Obligation: ur.Obligation, Private: ur.Private, Leftover: ur.Leftover,
		})
		if len(in.Incomes[ur.User]) == 0 {
			v.MissingIncome = append(v.MissingIncome, names[ur.User])
		}
	}
	for _, re := range st.Reimbursements {
		v.Reimbursements = append(v.Reimbursements, pages.Reimbursement{From: names[re.From], To: names[re.To], Amount: re.Amount})
	}
	for _, d := range st.Due {
		if d.Kind == ledger.Shared && d.Payer == ledger.Felleskonto {
			v.DueFromFelleskonto += d.Amount
		}
		v.Due = append(v.Due, pages.DueRow{Icon: components.CategoryIcon(d.Category, d.Name), Name: d.Name, Amount: d.Amount, Payer: payerLabel(d, names)})
	}
	v.BufferChange = v.Felleskonto - v.DueFromFelleskonto

	if v.IsCurrent {
		today := s.today()
		for _, usr := range users {
			fs, err := freeSpending(in, usr, today)
			if err != nil {
				serverError(w, r, err)
				return
			}
			v.Free = append(v.Free, fs)
		}
		if v.Buy, err = buyForm(in, u, today, input); err != nil {
			serverError(w, r, err)
			return
		}
	}

	upcoming, err := ledger.Upcoming(in, m, upcomingMonths)
	if err != nil {
		serverError(w, r, err)
		return
	}
	for _, b := range upcoming {
		v.Upcoming = append(v.Upcoming, pages.UpcomingRow{Icon: components.CategoryIcon(b.Category, b.Name), Name: b.Name,
			When: b.Month.Label(), Payer: payerLabel(b.DueItem, names), Amount: b.Amount})
	}
	plan, err := ledger.BufferNeed(in, m, bufferMonths)
	if err != nil {
		serverError(w, r, err)
		return
	}
	v.Buffer = pages.BufferView{Required: plan.Required, From: m.Label(), CanCheck: v.IsCurrent, Errors: map[string]string{}}
	if plan.Required > 0 {
		v.Buffer.LowNote = "Ellers går den i minus i " + plan.LowMonth.Label()
		if plan.LowBill.Name != "" {
			v.Buffer.LowNote += ", når " + plan.LowBill.Name + " skal betales"
		}
		v.Buffer.LowNote += "."
	}
	if input.bufferCheck {
		v.Buffer.Balance = input.fkBalance
		if bal, msg := parseAmount(input.fkBalance); msg != "" {
			v.Buffer.Errors["balance"] = msg
		} else if diff := bal - plan.Required; diff >= 0 {
			v.Buffer.Result = "Saldoen er " + money.Format(diff) + " over det bufferen bør være."
		} else {
			v.Buffer.Result = "Saldoen er " + money.Format(-diff) + " under det bufferen bør være. Vurder en ekstra overføring."
			v.Buffer.Short = true
		}
	}

	status := http.StatusOK
	if len(v.Buy.Errors) > 0 || len(v.Buffer.Errors) > 0 {
		status = http.StatusUnprocessableEntity
	}
	render(w, r, status, pages.Overview(v))
}

// payerLabel says who pays a due bill: "Felleskonto", "Ola betaler", "Kari (privat)".
func payerLabel(d ledger.DueItem, names map[ledger.UserID]string) string {
	switch {
	case d.Kind == ledger.Private:
		return names[d.Owner] + " (privat)"
	case d.Payer == ledger.Felleskonto:
		return "Felleskonto"
	default:
		return names[d.Payer] + " betaler"
	}
}

// payPeriod finds a user's current pay period and its free money (the leftover in
// the settlement of the period's budget month). ok is false without a payday.
func payPeriod(in ledger.Input, u store.User, today budget.Date) (p budget.Period, leftover money.Ore, ok bool, err error) {
	inc, has := ledger.IncomeAt(in.Incomes[u.ID], today.MonthOf())
	if !has || inc.Payday == 0 {
		return budget.Period{}, 0, false, nil
	}
	p = budget.PeriodAt(today, inc.Payday)
	st, err := ledger.Settle(in, p.BudgetMonth)
	if err != nil {
		return budget.Period{}, 0, false, err
	}
	r, _ := st.User(u.ID)
	return p, r.Leftover, true, nil
}

func freeSpending(in ledger.Input, u store.User, today budget.Date) (pages.FreeSpending, error) {
	fs := pages.FreeSpending{Name: u.DisplayName}
	p, leftover, ok, err := payPeriod(in, u, today)
	if err != nil || !ok {
		return fs, err
	}
	left := p.DaysLeft(today)
	fs.HasPayday = true
	fs.ShouldHave = budget.ShouldHave(leftover, p, today)
	fs.NormalPerDay = money.DivRound(leftover, int64(p.Days()))
	fs.NextPayday = nbDate(p.Next)
	fs.DaysLeft = days(left)
	fs.Days, fs.Elapsed = p.Days(), p.Days()-left
	return fs, nil
}

func buyForm(in ledger.Input, u store.User, today budget.Date, input overviewInput) (pages.BuyForm, error) {
	f := pages.BuyForm{Price: input.price, Balance: input.balance, Errors: map[string]string{}}
	p, leftover, ok, err := payPeriod(in, u, today)
	f.HasPayday = ok
	if err != nil || !ok || !input.buy {
		return f, err
	}
	price, msg := parseAmount(input.price)
	if msg != "" {
		f.Errors["price"] = msg
	}
	balance, msg := parseAmount(input.balance)
	if msg != "" {
		f.Errors["balance"] = msg
	}
	if len(f.Errors) == 0 {
		f.Result = verdictView(budget.Advise(balance, price, leftover, p, today))
	}
	return f, nil
}

// verdictView words an Advice in Norwegian.
func verdictView(a budget.Advice) *pages.Verdict {
	until := "til lønning om " + days(a.DaysLeft)
	v := &pages.Verdict{}
	switch a.Verdict {
	case budget.Fine:
		v.Kind, v.Icon, v.Headline = "fine", "check-circle", "Ja, det går fint."
		v.Lines = []string{
			"Etter kjøpet har du " + money.Format(a.After) + " " + until + ", " + money.Format(a.PerDay) + " per dag.",
			"Det er minst de " + money.Format(a.ShouldHave) + " et jevnt forbruk tilsier at du bør ha igjen nå.",
		}
	case budget.Tight:
		v.Kind, v.Icon, v.Headline = "tight", "warning-circle", "Ja, men det blir stramt."
		v.Lines = []string{
			"Etter kjøpet har du " + money.Format(a.After) + " " + until + ": " + money.Format(a.PerDay) +
				" per dag, mot vanlig " + money.Format(a.NormalPerDay) + ".",
		}
	case budget.RatherNot:
		v.Kind, v.Icon, v.Headline = "rather-not", "warning-circle", "Helst ikke nå."
		v.Lines = []string{
			"Da har du bare " + money.Format(a.PerDay) + " per dag " + until + " (vanlig: " + money.Format(a.NormalPerDay) + ").",
			"Etter lønning har du omtrent " + money.Format(a.Leftover) + " til fri bruk igjen.",
		}
	case budget.NotCovered:
		v.Kind, v.Icon, v.Headline = "not-covered", "x-circle", "Nei, saldoen dekker ikke kjøpet."
		v.Lines = []string{"Du mangler " + money.Format(a.Missing) + "."}
	case budget.NoBudget:
		v.Kind, v.Icon, v.Headline = "no-budget", "warning-circle", "Saldoen dekker det, men budsjettet går ikke opp."
		v.Lines = []string{"De faste utgiftene dine er større enn inntekten denne perioden, så det er ingen fri bruk å fordele."}
	}
	if a.BigPurchase {
		v.Lines = append(v.Lines, "Kjøpet er større enn en hel måneds fri bruk ("+money.Format(a.Leftover)+").")
	}
	return v
}
