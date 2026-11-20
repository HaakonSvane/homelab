// Package ledger is the settlement math: given every income and expense version, it
// works out for one month what each person transfers to the shared account
// (felleskonto), what they pay directly, any reimbursement between them, and what
// they have left. Pure functions only — no database or HTTP.
package ledger

import (
	"github.com/HaakonSvane/homelab/finance/internal/money"
	"github.com/HaakonSvane/homelab/finance/internal/month"
)

// UserID identifies a person. Real IDs are positive; 0 means "nobody".
type UserID int64

// Felleskonto as a payer means the bill is paid from the shared account.
const Felleskonto UserID = 0

// Kind says whether an expense is split between users or belongs to one of them.
type Kind string

const (
	Shared  Kind = "shared"
	Private Kind = "private"
)

// Share is one user's part of a shared expense, in basis points (5000 = 50.00 %).
type Share struct {
	User UserID
	BP   int
}

// ExpenseVersion is the expense's terms from ValidFrom until the next version.
type ExpenseVersion struct {
	ValidFrom month.Month
	Amount    money.Ore  // per occurrence, not per month
	Due       month.Mask // months the bill falls due
	Payer     UserID     // who pays the bill: Felleskonto, or a user paying directly (shared only)
	Shares    []Share    // shared only; must sum to 10000. Users not listed have 0.
}

// Expense is a recurring cost with its version history.
type Expense struct {
	ID       int64
	Name     string
	Category string
	Kind     Kind
	Owner    UserID      // private only
	End      month.Month // last month the expense applies (inclusive); zero = open-ended
	Versions []ExpenseVersion
}

// IncomeVersion is a user's net monthly income from ValidFrom until the next version.
type IncomeVersion struct {
	ValidFrom month.Month
	Amount    money.Ore
	Payday    int // day of month the income arrives, 1–31 (31 = last day); 0 = not set
}

// Input is everything the ledger needs: the users (in display order) and the full
// version history of incomes and expenses.
type Input struct {
	Users    []UserID
	Incomes  map[UserID][]IncomeVersion
	Expenses []Expense
}

// UserResult is one user's figures for the month. All amounts are monthly and
// smoothed (an expense due n times a year counts amount*n/12 every month).
type UserResult struct {
	User       UserID
	Income     money.Ore
	Obligation money.Ore // this user's share of all shared expenses
	PaidDirect money.Ore // shared bills this user pays directly
	Private    money.Ore // this user's private expenses (including savings)
	// Transfer is Obligation − PaidDirect, rounded so that the users' Transfers sum
	// exactly to Settlement.Felleskonto. Negative means the user is owed money.
	Transfer money.Ore
	// ToFelleskonto is what the user actually transfers to the shared account (≥ 0),
	// after reimbursements are taken out of it.
	ToFelleskonto money.Ore
	Leftover      money.Ore // Income − Obligation − Private
}

// Reimbursement is money one user pays the other directly, because the receiver pays
// more shared bills directly than their own share of all shared bills.
type Reimbursement struct {
	From, To UserID
	Amount   money.Ore
}

// DueItem is a bill that falls due in the settled month, at its actual (not
// smoothed) amount.
type DueItem struct {
	ExpenseID int64
	Name      string
	Category  string
	Kind      Kind
	Owner     UserID
	Payer     UserID
	Amount    money.Ore
	PerYear   int // how many months a year the bill falls due
}

// Settlement is the result for one month.
type Settlement struct {
	Month month.Month
	Users []UserResult // in Input.Users order
	// Felleskonto is the smoothed monthly total of shared bills paid from the shared
	// account: exactly what it receives each month (the sum of ToFelleskonto).
	Felleskonto    money.Ore
	Reimbursements []Reimbursement
	Due            []DueItem
}

// User returns the result for id, or false if id was not in the input.
func (s Settlement) User(id UserID) (UserResult, bool) {
	for _, u := range s.Users {
		if u.User == id {
			return u, true
		}
	}
	return UserResult{}, false
}
