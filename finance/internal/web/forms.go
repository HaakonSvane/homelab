package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// parsePercent reads a percentage with up to two decimals ("50", "62,5") as basis
// points. It reuses the money parser: 62,50 "kroner" is 6250 "øre", i.e. 6250 bp.
func parsePercent(raw string) (int, string) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	bp, err := money.Parse(raw)
	if err != nil || bp > 10000 || strings.ContainsAny(strings.ToLower(raw), "kr") {
		return 0, "Andelen må være et tall fra 0 til 100."
	}
	return int(bp), ""
}

// formatPercent shows basis points as a percentage without needless decimals: 50, 62,5.
func formatPercent(bp int) string {
	s := strconv.Itoa(bp/100) + "," + strconv.Itoa(bp%100/10) + strconv.Itoa(bp%10)
	s = strings.TrimSuffix(strings.TrimSuffix(s, "0"), "0")
	return strings.TrimSuffix(s, ",")
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
