package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web/components"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
)

func (s *Server) income(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m := s.monthParam(r)
	users, err := s.store.Users(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	incs, err := s.store.Incomes(ctx)
	if err != nil {
		serverError(w, r, err)
		return
	}
	names := userNames(users)
	v := pages.IncomeView{Nav: components.MonthNav{Path: "/inntekt", Month: m, Current: s.thisMonth()}}
	for _, u := range users {
		var versions []ledger.IncomeVersion
		var rows []store.Income
		for _, inc := range incs {
			if inc.User == u.ID {
				versions = append(versions, inc.IncomeVersion)
				rows = append(rows, inc)
			}
		}
		iu := pages.IncomeUser{ID: int64(u.ID), Name: u.DisplayName}
		cur, has := ledger.IncomeAt(versions, m)
		iu.Current, iu.Has = cur.Amount, has
		for i := len(rows) - 1; i >= 0; i-- { // newest first
			inc := rows[i]
			by := "registrert av " + names[inc.CreatedBy] + " " + components.Date(inc.CreatedAt)
			iu.History = append(iu.History, pages.IncomeRow{
				From: inc.ValidFrom.Label(), Amount: inc.Amount, Note: inc.Note, By: by,
				Current: has && inc.ValidFrom == cur.ValidFrom,
			})
		}
		v.Users = append(v.Users, iu)
	}
	render(w, r, http.StatusOK, pages.Income(v))
}

// incomeUser loads the user in the path ({id}).
func (s *Server) incomeUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return store.User{}, false
	}
	u, err := s.store.UserByID(r.Context(), ledger.UserID(id))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return store.User{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.User{}, false
	}
	return u, true
}

func (s *Server) incomeFormView(u store.User, amount, note string, from month.Month, errs map[string]string) pages.IncomeForm {
	return pages.IncomeForm{
		UserID: int64(u.ID), Name: u.DisplayName, Action: "/inntekt/" + strconv.FormatInt(int64(u.ID), 10),
		Amount: amount, Note: note, ValidFrom: monthOptions(from, s.thisMonth()), Errors: errs,
	}
}

func (s *Server) editIncome(w http.ResponseWriter, r *http.Request) {
	u, ok := s.incomeUser(w, r)
	if !ok {
		return
	}
	incs, err := s.store.Incomes(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	var versions []ledger.IncomeVersion
	for _, inc := range incs {
		if inc.User == u.ID {
			versions = append(versions, inc.IncomeVersion)
		}
	}
	now := s.thisMonth()
	amount := ""
	if cur, ok := ledger.IncomeAt(versions, now); ok {
		amount = money.FormatNumber(cur.Amount)
	}
	render(w, r, http.StatusOK, pages.IncomeFormPage(s.incomeFormView(u, amount, "", now, map[string]string{})))
}

func (s *Server) saveIncome(w http.ResponseWriter, r *http.Request) {
	u, ok := s.incomeUser(w, r)
	if !ok {
		return
	}
	rawAmount, note := r.PostFormValue("amount"), strings.TrimSpace(r.PostFormValue("note"))
	from, fromErr := month.Parse(r.PostFormValue("valid_from"))
	errs := map[string]string{}
	amount, msg := parseAmount(rawAmount)
	if msg != "" {
		errs["amount"] = msg
	}
	if fromErr != nil {
		errs["valid_from"] = "Velg måned."
	}
	if len([]rune(note)) > 200 {
		errs["note"] = "Notatet kan ha maks 200 tegn."
	}
	if len(errs) == 0 {
		err := s.store.SetIncome(r.Context(), u.ID, ledger.IncomeVersion{ValidFrom: from, Amount: amount}, note, currentUser(r).ID)
		if err == nil {
			to := "/inntekt"
			if from != s.thisMonth() {
				to += "?m=" + from.String()
			}
			redirectAfterPost(w, r, to)
			return
		}
		if !errors.Is(err, store.ErrInvalid) {
			serverError(w, r, err)
			return
		}
		errs["form"] = "Inntekten kunne ikke lagres. Sjekk feltene og prøv igjen."
	}
	render(w, r, http.StatusUnprocessableEntity, pages.IncomeFormPage(s.incomeFormView(u, rawAmount, note, from, errs)))
}
