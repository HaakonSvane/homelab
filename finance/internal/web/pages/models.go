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
	// account; BufferChange is Felleskonto minus that (negative: the buffer is drawn on).
	DueFromFelleskonto money.Ore
	BufferChange       money.Ore
	MissingIncome      []string // names of users without any registered income
	NoExpenses         bool

	IsCurrent bool           // the shown month is this month (the "now" sections show)
	Free      []FreeSpending // "Til fri bruk", one per person
	Buy       BuyForm        // "Kan jeg kjøpe?"
	Upcoming  []UpcomingRow  // big irregular bills in the next months; hidden if empty
	Buffer    BufferView     // "Buffer på felleskontoen"
}

// FreeSpending is one person's free money in the current pay period.
type FreeSpending struct {
	Name         string
	HasPayday    bool
	ShouldHave   money.Ore // what an even pace leaves today
	NormalPerDay money.Ore
	NextPayday   string // "fredag 18. desember"
	DaysLeft     string // "11 dager"
	Elapsed      int    // days into the period, for <progress>
	Days         int
	Note         string // set when the figures come from another month than the salary's
}

// BuyForm is the "Kan jeg kjøpe?" calculator. Nothing typed here is stored.
type BuyForm struct {
	HasPayday bool
	Price     string
	Balance   string
	Errors    map[string]string
	Result    *Verdict
}

// Verdict is the calculator's answer.
type Verdict struct {
	Kind     string // fine, tight, rather-not, not-covered, no-budget (CSS class and icon)
	Icon     string
	Headline string
	Lines    []string
}

// UpcomingRow is a big irregular bill in a coming month.
type UpcomingRow struct {
	Icon   string
	Name   string
	When   string // "mars 2027"
	Payer  string
	Amount money.Ore
}

// BufferView is how much the felleskonto should hold for the irregular bills.
type BufferView struct {
	Required money.Ore
	From     string // "november 2026"
	LowNote  string // "Ellers går den i minus i april 2027, når Bilforsikring skal betales."
	CanCheck bool   // offer the balance check (current month only)
	Balance  string
	Errors   map[string]string
	Result   string
	Short    bool // the typed balance is below the required buffer
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
	Icon   string
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

// ExpenseGroup is "Felles", "Dine utgifter" or "<Name>s utgifter", shown as an
// accordion section.
type ExpenseGroup struct {
	Title   string
	Monthly money.Ore
	Rows    []ExpenseRow
	Open    bool // expanded on load
}

// ExpenseRow is one expense as it stands in the shown month.
type ExpenseRow struct {
	ID      int64
	Icon    string // components.Icon name for the category
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
	Freq       string // monthly, semiannual, yearly, eleven or custom
	YearMonths []Option
	SemiMonths []Option // "januar og juli" … "juni og desember"
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

// DeleteConfirm is the no-JavaScript confirmation page for deleting an expense.
type DeleteConfirm struct {
	Name     string
	Action   string
	Versions int
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
	Payday  string // "Lønning den 20. · neste fredag 18. desember", or "" if not set
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
	Paydays   []Option
	ValidFrom []Option
	Errors    map[string]string
}
