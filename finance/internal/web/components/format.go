// Package components holds the shared templ components (page layout, masthead) and
// the nb-NO display helpers they use.
package components

import "time"

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

// htmxConfig turns off htmx's injected <style> (blocked by our CSP) and makes swaps
// use View Transitions where the browser supports them.
const htmxConfig = `{"includeIndicatorStyles":false,"globalViewTransitions":true}`

// Date formats as 19.11.2026 in local time (TZ=Europe/Oslo).
func Date(t time.Time) string { return t.In(time.Local).Format("02.01.2006") }

// DateTime formats as 19.11.2026 kl. 21:40 in local time.
func DateTime(t time.Time) string { return t.In(time.Local).Format("02.01.2006 kl. 15:04") }
