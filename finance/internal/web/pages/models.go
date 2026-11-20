package pages

import (
	"github.com/HaakonSvane/homelab/finance/internal/ledger"
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/web/components"
)

// View models: everything the templates show, already formatted or decided, so the
// templates stay free of logic.

// OverviewView is the Oversikt page.
type OverviewView struct {
	Greeting       string
	Nav            components.MonthNav
	Users          []OverviewUser
	Felleskonto    money.Ore // what the shared account receives this month
	Reimbursements []Reimbursement
	Due            []DueRow
	// DueFromFelleskonto is what the bills due this month take out of the shared
	// account; Buffer is Felleskonto minus that (negative: the buffer is drawn on).
	DueFromFelleskonto money.Ore
	Buffer             money.Ore
	MissingIncome      []string // names of users without any registered income
	NoExpenses         bool
}

// OverviewUser is one person's line on the overview.
type OverviewUser struct {
	Name          string
	ToFelleskonto money.Ore
	PaidDirect    money.Ore
	Income        money.Ore
	Obligation    money.Ore
	Private       money.Ore
	Leftover      money.Ore
}

// Reimbursement is "From skylder To Amount".
type Reimbursement struct {
	From, To string
	Amount   money.Ore
}

// DueRow is a bill due in the shown month.
type DueRow struct {
	Name   string
	Amount money.Ore
	Payer  string // "Felleskonto", "Haakon betaler", "Rikke (privat)"
}

// ExpensesView is the Utgifter page.
type ExpensesView struct {
	Nav      components.MonthNav
	Groups   []ExpenseGroup
	Inactive []ExpenseRow // ended before, or starting after, the shown month
	Empty    bool
}

// ExpenseGroup is "Felles" or "Privat · <name>".
type ExpenseGroup struct {
	Title   string
	Monthly money.Ore
	Rows    []ExpenseRow
}

// ExpenseRow is one expense as it stands in the shown month.
type ExpenseRow struct {
	ID      int64
	Name    string
	Details string // "Bolig · Halvårlig (mar og sep) · 4 800,00 kr per gang · 60/40 · Haakon betaler"
	Monthly money.Ore
	Status  string // for inactive rows: "Avsluttet etter juni 2026" / "Starter mars 2027"
}

// Option is a choice in a select or radio group.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// MonthBox is one of the twelve due-month checkboxes.
type MonthBox struct {
	Value   int // 1–12
	Label   string
	Checked bool
}

// ExpenseForm is the add/edit expense form, with the raw input echoed back on errors.
type ExpenseForm struct {
	ID         int64 // 0 for a new expense
	Action     string
	Name       string
	Category   string
	Categories []string // suggestions
	KindFixed  bool     // editing: kind and owner can't change
	Kind       ledger.Kind
	KindLabel  string // shown when fixed: "Felles" / "Privat (Rikke)"
	Owners     []Option
	Amount     string
	Months     []MonthBox
	Payers     []Option
	ShareA     string // percent for the first user, e.g. "50"
	UserA      string
	UserB      string
	ShareBHint string // "Rikke dekker 50 %"
	ValidFrom  []Option
	Ended      string // editing an ended expense: "juni 2026"
	EndMonths  []Option
	History    []string
	Errors     map[string]string
	Focus      string // the first field with an error; it gets autofocus
}

// expenseFieldOrder is the order fields appear in the form, for picking Focus.
var expenseFieldOrder = []string{"name", "category", "kind", "owner", "amount", "due", "share", "payer", "valid_from"}

// FirstError returns the first field (in form order) that has an error.
func FirstError(errs map[string]string) string {
	for _, f := range expenseFieldOrder {
		if errs[f] != "" {
			return f
		}
	}
	return ""
}

// IncomeView is the Inntekt page.
type IncomeView struct {
	Nav   components.MonthNav
	Users []IncomeUser
}

// IncomeUser is one person's income in the shown month, with history.
type IncomeUser struct {
	ID      int64
	Name    string
	Current money.Ore
	Has     bool
	History []IncomeRow
}

// IncomeRow is one income version.
type IncomeRow struct {
	From    string // "januar 2026"
	Amount  money.Ore
	Note    string
	By      string // "registrert av Haakon 20.11.2026"
	Current bool   // the version in effect in the shown month
}

// IncomeForm is the change-income form.
type IncomeForm struct {
	UserID    int64
	Name      string
	Action    string
	Amount    string
	Note      string
	ValidFrom []Option
	Errors    map[string]string
}
