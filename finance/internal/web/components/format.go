// Package components holds the shared templ components (page layout, masthead) and
// the nb-NO display helpers they use.
package components

import (
	"time"

	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// MonthNav drives the previous/next month navigation.
type MonthNav struct {
	Path    string      // page the links point to, e.g. "/" or "/utgifter"
	Month   month.Month // shown month
	Current month.Month // this month (Oslo time)
}

func (n MonthNav) href(m month.Month) string {
	if m == n.Current {
		return n.Path
	}
	return n.Path + "?m=" + m.String()
}

// Tab is the active section in the masthead; TabNone hides the masthead (login,
// enrollment, errors).
type Tab string

const (
	TabNone     Tab = ""
	TabOverview Tab = "oversikt"
	TabExpenses Tab = "utgifter"
	TabIncome   Tab = "inntekt"
	TabSettings Tab = "innstillinger"
)

// htmxConfig turns off htmx's injected <style> (blocked by our CSP), makes swaps use
// View Transitions where supported, and swaps 422 responses so a form re-rendered
// with validation errors replaces the submitted one (other 4xx/5xx are not swapped).
const htmxConfig = `{"includeIndicatorStyles":false,"globalViewTransitions":true,"responseHandling":[` +
	`{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"422","swap":true},` +
	`{"code":"[45]..","swap":false,"error":true},{"code":"...","swap":true}]}`

// Greeting greets name for the time of day of t (pass local time): God morgen
// (05–10), God formiddag (10–12), God ettermiddag (12–15), a plain "Hei, <name>!"
// in the late afternoon (15–18) where that is what people actually say, God kveld
// (18–24), and after midnight a wry "Sen økt, <name>?" ("God natt" is a goodbye).
func Greeting(t time.Time, name string) string {
	switch h := t.Hour(); {
	case h < 5:
		return "Sen økt, " + name + "?"
	case h < 10:
		return "God morgen, " + name
	case h < 12:
		return "God formiddag, " + name
	case h < 15:
		return "God ettermiddag, " + name
	case h < 18:
		return "Hei, " + name + "!"
	default:
		return "God kveld, " + name
	}
}

// Date formats as 19.11.2026 in local time (TZ=Europe/Oslo).
func Date(t time.Time) string { return t.In(time.Local).Format("02.01.2006") }

// DateTime formats as 19.11.2026 kl. 21:40 in local time.
func DateTime(t time.Time) string { return t.In(time.Local).Format("02.01.2006 kl. 15:04") }

// Kr formats an amount: 12 345,00 kr.
func Kr(o money.Ore) string { return money.Format(o) }

// SignedKr formats a change with an explicit sign: +1 200,00 kr / −300,00 kr.
func SignedKr(o money.Ore) string {
	if o > 0 {
		return "+" + money.Format(o)
	}
	return money.Format(o)
}

// Amount formats an amount without the currency, for ledger columns: 12 345,00.
func Amount(o money.Ore) string { return money.FormatNumber(o) }
