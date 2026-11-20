package web

import (
	"net/http"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/web/components"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
)

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	m := s.monthParam(r)
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
		row := pages.DueRow{Icon: components.CategoryIcon(d.Category, d.Name), Name: d.Name, Amount: d.Amount}
		switch {
		case d.Kind == ledger.Private:
			row.Payer = names[d.Owner] + " (privat)"
		case d.Payer == ledger.Felleskonto:
			row.Payer = "Felleskonto"
			v.DueFromFelleskonto += d.Amount
		default:
			row.Payer = names[d.Payer] + " betaler"
		}
		v.Due = append(v.Due, row)
	}
	v.Buffer = v.Felleskonto - v.DueFromFelleskonto
	render(w, r, http.StatusOK, pages.Overview(v))
}
