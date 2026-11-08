package api

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/event"
)

func TestFormatTime(t *testing.T) {
	if FormatTime(time.Time{}) != "" {
		t.Error("zero time must be empty")
	}
	at := time.Date(2026, 11, 6, 23, 2, 11, 123456789, time.FixedZone("x", 7*3600))
	if got := FormatTime(at); got != "2026-11-06T16:02:11.123Z" {
		t.Errorf("FormatTime = %q", got)
	}
	back, err := ParseTime(FormatTime(at))
	if err != nil || !back.Equal(at.Truncate(time.Millisecond)) {
		t.Errorf("ParseTime = %v, %v", back, err)
	}
}

func TestUsageOf(t *testing.T) {
	if UsageOf(nil) != nil || UsageOf(&event.Usage{}) != nil {
		t.Error("no usage must be nil")
	}
	u := UsageOf(&event.Usage{InputTokens: 3, OutputTokens: 4})
	if u.TotalTokens != 7 {
		t.Errorf("total = %d", u.TotalTokens)
	}
}

// The mappers that build these shapes are tested where they live (task, role, memory): lists never null.
func TestEventTypes(t *testing.T) {
	if len(EventTypes()) != 26 {
		t.Errorf("%d event types", len(EventTypes()))
	}
}

// The TypeScript union of LoginSession.state is exactly the Go constants.
func TestLoginStatesMatchTSUnion(t *testing.T) {
	f, ok := reflect.TypeFor[LoginSession]().FieldByName("State")
	if !ok {
		t.Fatal("LoginSession has no State")
	}
	quoted := make([]string, 0, len(LoginStates()))
	for _, s := range LoginStates() {
		quoted = append(quoted, "'"+s+"'")
	}
	if got, want := f.Tag.Get("ts"), strings.Join(quoted, " | "); got != want {
		t.Errorf("ts tag %q, want %q", got, want)
	}
}
