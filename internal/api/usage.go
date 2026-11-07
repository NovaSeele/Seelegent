package api

// The usage shapes mirror internal/usage (Summary, History and their parts) field by field, with times as API
// strings (FormatTime). Labels are never email addresses: internal/usage replaces them with the account id.

// Totals mirrors internal/usage.Totals.
type Totals struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
	// HasCost is true when some attempt reported a cost.
	HasCost  bool `json:"hasCost"`
	Tasks    int  `json:"tasks"`
	Turns    int  `json:"turns"`
	Attempts int  `json:"attempts"`
}

// DayTotal mirrors internal/usage.DayTotal.
type DayTotal struct {
	Date    string  `json:"date"`
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
	Tasks   int     `json:"tasks"`
}

// TurnUsage mirrors internal/usage.TurnUsage.
type TurnUsage struct {
	N          int     `json:"n"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"costUsd"`
	DurationMS int64   `json:"durationMs"`
	Attempts   int     `json:"attempts"`
	At         string  `json:"at"`
	AccountID  string  `json:"accountId"`
	Account    string  `json:"account"`
	Model      string  `json:"model"`
}

// TaskUsage mirrors internal/usage.TaskUsage.
type TaskUsage struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Status     string      `json:"status"`
	Backend    string      `json:"backend"`
	Tokens     int64       `json:"tokens"`
	CostUSD    float64     `json:"costUsd"`
	DurationMS int64       `json:"durationMs"`
	LastAt     string      `json:"lastAt"`
	AccountID  string      `json:"accountId"`
	Account    string      `json:"account"`
	Turns      int         `json:"turns"`
	Items      []TurnUsage `json:"items"`
	More       int         `json:"more"`
}

// ProjectUsage mirrors internal/usage.ProjectUsage.
type ProjectUsage struct {
	Key        string      `json:"key"`
	Name       string      `json:"name"`
	Tokens     int64       `json:"tokens"`
	CostUSD    float64     `json:"costUsd"`
	DurationMS int64       `json:"durationMs"`
	Tasks      int         `json:"tasks"`
	Turns      int         `json:"turns"`
	Items      []TaskUsage `json:"items"`
	More       int         `json:"more"`
}

// ModelUsage mirrors internal/usage.ModelUsage.
type ModelUsage struct {
	Model      string  `json:"model"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"costUsd"`
	Attempts   int     `json:"attempts"`
	DurationMS int64   `json:"durationMs"`
}

// AccountUsage mirrors internal/usage.AccountUsage.
type AccountUsage struct {
	ID         string       `json:"id"`
	Label      string       `json:"label"`
	Backend    string       `json:"backend"`
	Tokens     int64        `json:"tokens"`
	CostUSD    float64      `json:"costUsd"`
	Attempts   int          `json:"attempts"`
	DurationMS int64        `json:"durationMs"`
	Models     []ModelUsage `json:"models"`
	More       int          `json:"more"`
}

// Summary mirrors internal/usage.Summary (GET /usage/summary).
type Summary struct {
	Days          int    `json:"days"`
	Since         string `json:"since"`
	Until         string `json:"until"`
	PreviousSince string `json:"previousSince"`
	// DataSince is the oldest attempt kept; absent when there is none.
	DataSince    string         `json:"dataSince,omitempty"`
	Totals       Totals         `json:"totals"`
	Previous     Totals         `json:"previous"`
	Daily        []DayTotal     `json:"daily"`
	Projects     []ProjectUsage `json:"projects"`
	MoreProjects int            `json:"moreProjects"`
	Accounts     []AccountUsage `json:"accounts"`
	MoreAccounts int            `json:"moreAccounts"`
}

// UsageWindow mirrors internal/usage.Window: one quota window in a usage snapshot.
type UsageWindow struct {
	Group     string  `json:"group"`
	Window    string  `json:"window"`
	Label     string  `json:"label,omitempty"`
	Remaining float64 `json:"remaining"`
	ResetAt   string  `json:"resetAt,omitempty"`
	// MinRemaining is the lowest remaining of the day (daily rollup only).
	MinRemaining *float64 `json:"minRemaining,omitempty"`
}

// UsageSnapshot mirrors internal/usage.Snapshot: one account at one hour.
type UsageSnapshot struct {
	At        string        `json:"at"`
	AccountID string        `json:"accountId"`
	Backend   string        `json:"backend"`
	Label     string        `json:"label"`
	Enabled   bool          `json:"enabled"`
	Status    string        `json:"status"`
	Plan      string        `json:"plan,omitempty"`
	Quota     []UsageWindow `json:"quota"`
	// TokensToday and CostToday are the usage on the snapshot's local day so far.
	TokensToday int64   `json:"tokensToday"`
	CostToday   float64 `json:"costToday"`
}

// Day mirrors internal/usage.Day: one account on one local day.
type Day struct {
	Date        string        `json:"date"`
	AccountID   string        `json:"accountId"`
	Backend     string        `json:"backend"`
	Label       string        `json:"label"`
	Enabled     bool          `json:"enabled"`
	Status      string        `json:"status"`
	Plan        string        `json:"plan,omitempty"`
	Samples     int           `json:"samples"`
	FirstAt     string        `json:"firstAt"`
	LastAt      string        `json:"lastAt"`
	Quota       []UsageWindow `json:"quota"`
	TokensToday int64         `json:"tokensToday"`
	CostToday   float64       `json:"costToday"`
}

// History mirrors internal/usage.History (GET /usage/history).
type History struct {
	Bucket string `json:"bucket" ts:"'hour' | 'day'"`
	Since  string `json:"since"`
	Until  string `json:"until"`
	// FirstAt is the oldest snapshot kept; absent when there is none.
	FirstAt string `json:"firstAt,omitempty"`
	// Rows is set for bucket hour, Days for bucket day.
	Rows []UsageSnapshot `json:"rows,omitempty"`
	Days []Day           `json:"days,omitempty"`
}
