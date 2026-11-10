package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAddLessonCreatesTheSection(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)

	// Empty notes: the section is the whole text.
	dir := t.TempDir()
	n, err := s.AddLesson(ctx, dir, "  Run   go vet\r\nbefore you report.  ")
	if want := LessonsHeading + "\n- Run go vet before you report.\n"; err != nil || n.Text != want {
		t.Fatalf("AddLesson on empty notes = %q, %v; want %q", n.Text, err, want)
	}

	// Notes without the section: it goes at the end, after a blank line.
	dir = t.TempDir()
	if _, err := s.Append(ctx, dir, "Use pnpm."); err != nil {
		t.Fatal(err)
	}
	n, err = s.AddLesson(ctx, dir, "- Never edit generated files.")
	if want := "Use pnpm.\n\n" + LessonsHeading + "\n- Never edit generated files.\n"; err != nil || n.Text != want {
		t.Fatalf("AddLesson = %q, %v; want %q", n.Text, err, want)
	}
	if got, err := s.Get(ctx, dir); err != nil || got.Text != n.Text {
		t.Fatalf("Get = %q, %v", got.Text, err)
	}

	// A heading in fenced code is not the section; notes ending in a blank line get no second one.
	dir = t.TempDir()
	fenced := "```md\n" + LessonsHeading + "\n```\n\n"
	if _, err := s.Replace(ctx, dir, fenced); err != nil {
		t.Fatal(err)
	}
	n, err = s.AddLesson(ctx, dir, "x")
	if want := "```md\n" + LessonsHeading + "\n```\n\n" + LessonsHeading + "\n- x\n"; err != nil || n.Text != want {
		t.Fatalf("AddLesson after fence = %q, %v; want %q", n.Text, err, want)
	}
}

func TestAddLessonAppendsToTheSection(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	notes := "Intro.\n\n" + LessonsHeading + "\n- First rule.\n\n## Commands\n\ngo test ./...\n"
	if _, err := s.Replace(ctx, dir, notes); err != nil {
		t.Fatal(err)
	}
	n, err := s.AddLesson(ctx, dir, "Second rule.")
	want := "Intro.\n\n" + LessonsHeading + "\n- First rule.\n- Second rule.\n\n## Commands\n\ngo test ./...\n"
	if err != nil || n.Text != want {
		t.Fatalf("AddLesson before another section = %q, %v; want %q", n.Text, err, want)
	}
	// The section at the end; a level-3 heading inside it does not end it.
	if _, err := s.Replace(ctx, dir, "# Notes\n"+LessonsHeading+"\n### Go\n- One.\n"); err != nil {
		t.Fatal(err)
	}
	n, err = s.AddLesson(ctx, dir, "Two.")
	if want := "# Notes\n" + LessonsHeading + "\n### Go\n- One.\n- Two.\n"; err != nil || n.Text != want {
		t.Fatalf("AddLesson at the end = %q, %v; want %q", n.Text, err, want)
	}
	// CRLF notes keep their line ends.
	if _, err := s.Replace(ctx, dir, LessonsHeading+"\r\n- One.\r\n\r\n## Next\r\n"); err != nil {
		t.Fatal(err)
	}
	n, err = s.AddLesson(ctx, dir, "Two.")
	if want := LessonsHeading + "\r\n- One.\r\n- Two.\r\n\r\n## Next\r\n"; err != nil || n.Text != want {
		t.Fatalf("AddLesson on CRLF = %q, %v; want %q", n.Text, err, want)
	}
}

func TestAddLessonDuplicateIsNoOp(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	first, err := s.AddLesson(ctx, dir, "Run the linter three times.")
	if err != nil {
		t.Fatal(err)
	}
	for _, again := range []string{"run the LINTER three times.", "  - Run the linter\nthree times.  "} {
		n, err := s.AddLesson(ctx, dir, again)
		if err != nil || n.Text != first.Text || !n.UpdatedAt.Equal(first.UpdatedAt) {
			t.Fatalf("duplicate %q = %+v, %v; want %+v", again, n, err, first)
		}
	}
	// The same words outside the section are not a duplicate.
	if _, err := s.Replace(ctx, dir, "- Keep it short.\n\n"+LessonsHeading+"\n"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.AddLesson(ctx, dir, "Keep it short."); err != nil ||
		n.Text != "- Keep it short.\n\n"+LessonsHeading+"\n- Keep it short.\n" {
		t.Fatalf("rule outside the section = %q, %v", n.Text, err)
	}
}

func TestAddLessonRefusesBadRules(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	if _, err := s.Append(ctx, dir, "Keep."); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", " \n\t ", "- ", strings.Repeat("x", MaxLessonBytes+1)} {
		if _, err := s.AddLesson(ctx, dir, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddLesson(%.20q) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := s.AddLesson(ctx, dir, strings.Repeat("x", MaxLessonBytes)); err != nil {
		t.Errorf("rule at the limit = %v", err)
	}
	if _, err := s.AddLesson(ctx, "relative", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("relative cwd = %v", err)
	}
}

func TestAddLessonOverTheCap(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	full := strings.Repeat("x", MaxBytes-20) + "\n"
	before, err := s.Replace(ctx, dir, full)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLesson(ctx, dir, "A rule that does not fit."); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the cap = %v", err)
	}
	if got, err := s.Get(ctx, dir); err != nil || got.Text != full || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("notes after a refused lesson = %d bytes at %v, %v", len(got.Text), got.UpdatedAt, err)
	}
}

func TestCountVerdicts(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	if m, n, err := s.CountVerdicts(ctx, dir, 10, "rejected"); err != nil || m != 0 || n != 0 {
		t.Fatalf("no history = %d of %d, %v", m, n, err)
	}
	// Oldest first: 2 rejected that fall out of the window, then 12 with 3 rejected.
	verdicts := []string{"rejected", "rejected", "accepted", "accepted", "rejected", "accepted", "accepted",
		"rejected", "accepted", "accepted", "accepted", "rejected", "accepted", "accepted"}
	for i, v := range verdicts {
		if err := s.AddVerdict(ctx, Verdict{Cwd: dir, TaskID: fmt.Sprintf("t%d", i), Verdict: v}); err != nil {
			t.Fatal(err)
		}
	}
	if m, n, err := s.CountVerdicts(ctx, dir, 10, "rejected"); err != nil || m != 3 || n != 10 {
		t.Fatalf("last 10 = %d of %d, %v", m, n, err)
	}
	if m, n, err := s.CountVerdicts(ctx, dir, 100, "rejected"); err != nil || m != 5 || n != len(verdicts) {
		t.Fatalf("all = %d of %d, %v", m, n, err)
	}
	if m, n, err := s.CountVerdicts(ctx, t.TempDir(), 10, "rejected"); err != nil || m != 0 || n != 0 {
		t.Fatalf("other project = %d of %d, %v", m, n, err)
	}
	if _, _, err := s.CountVerdicts(ctx, "rel", 10, "rejected"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relative cwd = %v", err)
	}
}
