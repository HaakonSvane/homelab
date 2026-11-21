package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/budget"
	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
	"github.com/HaakonSvane/homelab/finance/internal/store"
	"github.com/HaakonSvane/homelab/finance/internal/web/pages"
)

// thisMonth is the current month in Oslo time (TZ is set in the container).
func (s *Server) thisMonth() month.Month { return month.Of(s.store.Now().In(time.Local)) }

// monthParam reads ?m=YYYY-MM, defaulting to this month.
func (s *Server) monthParam(r *http.Request) month.Month {
	if m, err := month.Parse(r.URL.Query().Get("m")); err == nil {
		return m
	}
	return s.thisMonth()
}

// parseAmount reads a money field, with a Norwegian message on failure.
func parseAmount(raw string) (money.Ore, string) {
	if strings.TrimSpace(raw) == "" {
		return 0, "Fyll inn beløpet."
	}
	o, err := money.Parse(raw)
	switch {
	case errors.Is(err, money.ErrNegative):
		return 0, "Beløpet kan ikke være negativt."
	case errors.Is(err, money.ErrPrecision):
		return 0, "Bruk høyst to desimaler."
	case errors.Is(err, money.ErrTooLarge):
		return 0, "Beløpet er for stort."
	case err != nil:
		return 0, "Skriv beløpet som for eksempel 1 250 eller 1 250,50."
	}
	return o, ""
}

// monthOptions offers months from January two years back to December next year,
// plus selected if it falls outside that range.
func monthOptions(selected, now month.Month) []pages.Option {
	first, last := month.New(now.Year()-2, time.January), month.New(now.Year()+1, time.December)
	var out []pages.Option
	if !selected.IsZero() && selected < first {
		out = append(out, monthOption(selected, selected))
	}
	for m := first; m <= last; m++ {
		out = append(out, monthOption(m, selected))
	}
	if selected > last {
		out = append(out, monthOption(selected, selected))
	}
	return out
}

func monthOption(m, selected month.Month) pages.Option {
	return pages.Option{Value: m.String(), Label: m.Label(), Selected: m == selected}
}

// userNames maps user IDs to display names.
func userNames(users []store.User) map[ledger.UserID]string {
	names := make(map[ledger.UserID]string, len(users))
	for _, u := range users {
		names[u.ID] = u.DisplayName
	}
	return names
}

// isHTMX reports whether the request came from htmx (for redirects after a POST).
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

// redirectAfterPost finishes a successful form POST: a 303 for plain forms, or an
// HX-Redirect for htmx so the whole page reloads (and the sheet closes).
func redirectAfterPost(w http.ResponseWriter, r *http.Request, to string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

var weekdays = [7]string{"søndag", "mandag", "tirsdag", "onsdag", "torsdag", "fredag", "lørdag"}

// nbDate formats a date the Norwegian way with the weekday: "fredag 18. desember".
func nbDate(d budget.Date) string {
	return weekdays[d.Weekday()] + " " + strconv.Itoa(d.Day) + ". " + month.Name(d.Month)
}

// days formats a day count: "1 dag", "11 dager".
func days(n int) string {
	if n == 1 {
		return "1 dag"
	}
	return strconv.Itoa(n) + " dager"
}

// paydayLabel describes a payday setting: "den 20.", "siste dag i måneden".
func paydayLabel(day int) string {
	if day == 31 {
		return "siste dag i måneden"
	}
	return "den " + strconv.Itoa(day) + "."
}

// paydayOptions offers "Ikke satt", the 1st–30th and the last day of the month.
func paydayOptions(selected int) []pages.Option {
	out := []pages.Option{{Value: "0", Label: "Ikke satt", Selected: selected == 0}}
	for day := 1; day <= 31; day++ {
		label := paydayLabel(day)
		if day == 31 {
			label = "Siste dag i måneden"
		}
		out = append(out, pages.Option{Value: strconv.Itoa(day), Label: label, Selected: day == selected})
	}
	return out
}

// today is the current date in Oslo.
func (s *Server) today() budget.Date { return budget.DateOf(s.store.Now().In(time.Local)) }
