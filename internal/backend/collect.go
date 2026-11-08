package backend

import (
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// OutcomeStatus is how a turn ended, from the task engine's point of view.
type OutcomeStatus string

// Outcome statuses.
const (
	// OutcomeOK: the CLI reported success.
	OutcomeOK OutcomeStatus = "ok"
	// OutcomeFailed: the turn failed; Class says how to react.
	OutcomeFailed OutcomeStatus = "failed"
	// OutcomeBackgroundWait: the agent ended its own steps while a background command it started still ran.
	OutcomeBackgroundWait OutcomeStatus = "background_wait"
	// OutcomeCanceled: Seelegent canceled the turn.
	OutcomeCanceled OutcomeStatus = "canceled"
)

// Outcome summarizes a finished turn.
type Outcome struct {
	Status OutcomeStatus `json:"status"`
	// Class is set when Status is failed.
	Class event.Class `json:"class,omitempty"`
	// Error is the failure message (secrets masked).
	Error string `json:"error,omitempty"`
	// ResetAt is when the limit resets, when Class is quota_exhausted and Error says so ("resets 3:10am
	// (Asia/Ho_Chi_Minh)"); zero otherwise. The task engine passes it to the pool as the rest's hint.
	ResetAt time.Time `json:"resetAt,omitzero"`
	// Response is the agent's final answer.
	Response string `json:"response,omitempty"`
	// ConversationID resumes the conversation.
	ConversationID string `json:"conversationId,omitempty"`
	// Model is the model the CLI reported.
	Model string `json:"model,omitempty"`
	// FilesEdited and FilesRead list paths in first-seen order, without duplicates.
	FilesEdited []string `json:"filesEdited,omitempty"`
	FilesRead   []string `json:"filesRead,omitempty"`
	// Commands are the shell commands the agent ran; BlockedCommands those Seelegent's guard refused.
	Commands        []string `json:"commands,omitempty"`
	BlockedCommands []string `json:"blockedCommands,omitempty"`
	// DeniedActions are actions the CLI's own permission system refused.
	DeniedActions []string `json:"deniedActions,omitempty"`
	// Usage is the turn's token usage (camelCase JSON, see B12).
	Usage event.Usage `json:"usage"`
	// ThreadUsage is the conversation's usage total the CLI reported, for a CLI whose usage counts every earlier run
	// of a resumed conversation (codex); Usage is then this run's part. The next run of the conversation starts from
	// it.
	ThreadUsage *event.Usage `json:"threadUsage,omitempty"`
	// ThreadTracked is set on every outcome of such a CLI, with or without ThreadUsage: Usage is this run's part.
	// An outcome without it was recorded before Seelegent kept the total, and its Usage was the CLI's total.
	ThreadTracked bool `json:"threadTracked,omitempty"`
	// DurationMs is the duration the CLI reported.
	DurationMs int64 `json:"durationMs,omitempty"`
	// BackgroundWait is the CLI's line when Status is background_wait.
	BackgroundWait string `json:"backgroundWait,omitempty"`
}

// Collector folds a turn's events into its Outcome. Feed it every event the parser emits, in order.
type Collector struct {
	o         Outcome
	report    *event.Report
	failure   *event.Error
	stepUsage event.Usage
	waiting   string
}

// Add takes one event.
func (c *Collector) Add(ev event.Event) {
	switch ev.Kind {
	case event.KindSession:
		if ev.Session.ConversationID != "" {
			c.o.ConversationID = ev.Session.ConversationID
		}
		if ev.Session.Model != "" {
			c.o.Model = ev.Session.Model
		}
	case event.KindToolCall, event.KindToolResult:
		c.waiting = ""
		t := ev.Tool
		switch {
		case t.Blocked && t.Command != "":
			c.o.BlockedCommands = appendUnique(c.o.BlockedCommands, t.Command)
		case ev.Kind == event.KindToolResult && t.State == event.ToolDone && t.Command != "":
			c.o.Commands = append(c.o.Commands, t.Command)
		}
	case event.KindFile:
		if ev.File.Op == event.FileEdit {
			c.o.FilesEdited = appendUnique(c.o.FilesEdited, ev.File.Path)
		} else {
			c.o.FilesRead = appendUnique(c.o.FilesRead, ev.File.Path)
		}
	case event.KindMessage:
		c.waiting = ""
	case event.KindUsage:
		c.stepUsage = c.stepUsage.Add(*ev.Usage)
	case event.KindReport:
		c.report = ev.Report
	case event.KindError:
		c.failure = ev.Error
	case event.KindBackgroundWait:
		c.waiting = ev.Text
	case event.KindLog:
	}
}

// Outcome returns the outcome of the turn that ended with exit.
func (c *Collector) Outcome(exit launch.Exit) Outcome {
	o := c.o
	o.Commands = slices.Clone(o.Commands)
	o.Usage = c.stepUsage
	if r := c.report; r != nil {
		o.Response = r.Response
		if r.ConversationID != "" {
			o.ConversationID = r.ConversationID
		}
		// The report's total wins over the per-step usage events, which it already includes.
		if r.Usage != nil {
			o.Usage = *r.Usage
		}
		o.DurationMs = r.DurationMs
		o.DeniedActions = r.DeniedActions
	}
	switch {
	case c.report != nil && c.report.Status == event.StatusSuccess:
		o.Status = OutcomeOK
	case exit.Reason == launch.ReasonCanceled:
		o.Status = OutcomeCanceled
		o.Error = "canceled"
	case c.failure != nil:
		o.Status = OutcomeFailed
		o.Class = c.failure.Class
		o.Error = c.failure.Message
		if o.Class == event.ClassQuotaExhausted {
			o.ResetAt = resetHint(o.Error, exit.EndedAt)
		}
	case c.waiting != "":
		o.Status = OutcomeBackgroundWait
		o.BackgroundWait = c.waiting
	default:
		o.Status = OutcomeFailed
		o.Class = event.ClassFatal
		o.Error = "turn ended without a result"
		if c.report != nil && c.report.Error != "" {
			o.Error = c.report.Error
		}
	}
	return o
}

func appendUnique(list []string, s string) []string {
	if s == "" || slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
