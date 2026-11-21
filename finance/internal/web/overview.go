package web

import (
	"errors"
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

// How far ahead the outlook sections look: the buffer over a year, and the big
// irregular bills in the shown month plus the next three.
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
	if v.Buffer, err = s.bufferView(in, m, v.IsCurrent, input); err != nil {
		serverError(w, r, err)
		return
	}

	status := http.StatusOK
	if len(v.Buy.Errors) > 0 || len(v.Buffer.Errors) > 0 {
		status = http.StatusUnprocessableEntity
	}
	render(w, r, status, pages.Overview(v))
}

// bufferView works out how much the felleskonto must hold. With both paydays it
// follows the actual dates (transfers on pay dates, bills on due days), from
// tomorrow for the current month (today's transactions are taken as done) or from
// the 1st for another month. Without paydays it falls back to the monthly estimate.
func (s *Server) bufferView(in ledger.Input, m month.Month, current bool, input overviewInput) (pages.BufferView, error) {
	bv := pages.BufferView{CanCheck: current, Errors: map[string]string{}}
	from := budget.Date{Year: m.Year(), Month: m.Month(), Day: 1}
	if current {
		from = s.today().AddDays(1)
	}
	dated, err := budget.FelleskontoBuffer(in, from)
	switch {
	case errors.Is(err, budget.ErrNoPayday):
		monthly, err := ledger.BufferNeed(in, m, bufferMonths)
		if err != nil {
			return bv, err
		}
		bv.Required = monthly.Required
		if bv.Required == 0 {
			bv.Headline = "Overføringene dekker alle regningene de neste tolv månedene, så det trengs ingen ekstra buffer."
		} else {
			bv.Headline = "Ved inngangen til " + m.Label() + " bør felleskontoen ha minst " + money.Format(bv.Required) +
				" før månedens overføringer kommer inn."
		}
		bv.Detail = "Et grovt anslag per måned. Legg inn lønningsdag for begge under Inntekt, så regnes bufferen med datoene pengene faktisk går inn og ut."
	case err != nil:
		return bv, err
	default:
		bv.Required = dated.Required
		switch {
		case bv.Required == 0:
			bv.Headline = "Overføringene kommer i tide til alle regningene de neste tolv månedene, så det trengs ingen ekstra buffer."
		case current:
			bv.Headline = "Felleskontoen bør ha minst " + money.Format(bv.Required) + " nå."
		default:
			bv.Headline = "Ved inngangen til " + m.Label() + " bør felleskontoen ha minst " + money.Format(bv.Required) + "."
		}
		bv.Detail = "Regnet med datoene for overføringer og trekk de neste tolv månedene"
		if current {
			bv.Detail += ", etter dagens transaksjoner"
		}
		bv.Detail += "."
		if bv.Required > 0 {
			bv.Detail += " Lavest " + longDate(dated.LowDate, s.today().Year) + ", når " + dated.LowBill + " trekkes."
		}
	}
	if input.bufferCheck {
		bv.Balance = input.fkBalance
		if bal, msg := parseAmount(input.fkBalance); msg != "" {
			bv.Errors["balance"] = msg
		} else if diff := bal - bv.Required; diff >= 0 {
			bv.Result = "Saldoen er " + money.Format(diff) + " over det bufferen bør være."
		} else {
			bv.Result = "Saldoen er " + money.Format(-diff) + " under det bufferen bør være. Vurder en ekstra overføring."
			bv.Short = true
		}
	}
	return bv, nil
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

// payPeriod finds a user's current pay period and its free money: the leftover in
// the settlement of the month whose salary started the period. ok is false without
// a payday.
//
// Before payday, the period started on last month's salary. If that month predates
// what has been registered (incomes and expenses usually start "from this month"),
// there are no figures for it, and its leftover would be a meaningless 0; the first
// month with figures is used instead, and figuresFrom says so (zero otherwise).
func payPeriod(in ledger.Input, u store.User, today budget.Date) (p budget.Period, leftover money.Ore, figuresFrom month.Month, ok bool, err error) {
	inc, has := ledger.IncomeAt(in.Incomes[u.ID], today.MonthOf())
	if !has || inc.Payday == 0 {
		return budget.Period{}, 0, 0, false, nil
	}
	p = budget.PeriodAt(today, inc.Payday)
	m := p.BudgetMonth
	if start := recordsStart(in, u.ID); m < start {
		m, figuresFrom = start, start
	}
	st, err := ledger.Settle(in, m)
	if err != nil {
		return budget.Period{}, 0, 0, false, err
	}
	r, _ := st.User(u.ID)
	return p, r.Leftover, figuresFrom, true, nil
}

// recordsStart is the first month with figures for a user: the later of their first
// income and the first expense. A leftover before then would leave something out.
func recordsStart(in ledger.Input, user ledger.UserID) month.Month {
	var start month.Month
	for _, v := range in.Incomes[user] {
		if start.IsZero() || v.ValidFrom < start {
			start = v.ValidFrom
		}
	}
	var firstExpense month.Month
	for _, e := range in.Expenses {
		for _, v := range e.Versions {
			if firstExpense.IsZero() || v.ValidFrom < firstExpense {
				firstExpense = v.ValidFrom
			}
		}
	}
	return max(start, firstExpense)
}

func freeSpending(in ledger.Input, u store.User, today budget.Date) (pages.FreeSpending, error) {
	fs := pages.FreeSpending{Name: u.DisplayName}
	p, leftover, figuresFrom, ok, err := payPeriod(in, u, today)
	if err != nil || !ok {
		return fs, err
	}
	if !figuresFrom.IsZero() {
		fs.Note = "Regnet ut fra tallene for " + month.Name(figuresFrom.Month()) + ", siden det ikke er registrert noe for " +
			month.Name(p.BudgetMonth.Month()) + "."
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
	p, leftover, _, ok, err := payPeriod(in, u, today)
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
