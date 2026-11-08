package ticket

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// failingDB is a store whose reads fail.
type failingDB struct{ DB }

var errDBDown = errors.New("database is locked")

func (failingDB) Read(context.Context, func(*sql.Tx) error) error { return errDBDown }

// TestOwnPushReturnsReadError is F65: an outbox that could not be read made ownPush answer "not ours", and the push
// was refused as branch_diverged. The read error comes back instead.
func TestOwnPushReturnsReadError(t *testing.T) {
	s := &Service{o: Options{DB: failingDB{}}}
	base, own, err := s.ownPush(t.Context(), "tk1", "0123456789abcdef0123456789abcdef01234567")
	if !errors.Is(err, errDBDown) || own || base != "" {
		t.Fatalf("ownPush = %q, %v, %v; want the read error", base, own, err)
	}
	if _, own, err := s.ownPush(t.Context(), "tk1", ""); err != nil || own {
		t.Fatalf("no sha: %v %v", own, err)
	}
}
