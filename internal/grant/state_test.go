package grant

import (
	"errors"
	"testing"
)

func TestNextValid(t *testing.T) {
	tests := []struct {
		from State
		ev   Event
		want State
	}{
		{Requested, NeedsApproval, PendingApproval},
		{Requested, AutoApprove, Approved},
		{Requested, Deny, Denied},
		{PendingApproval, Approve, Approved},
		{PendingApproval, Deny, Denied},
		{Approved, StartGrant, Granting},
		{Granting, Granted, Active},
		{Granting, Fail, Revoking}, // outcome unknown: fail closed and clean up
		{Active, Expire, Revoking},
		{Active, Revoke, Revoking},
		{Revoking, RevokeDone, Revoked},
	}
	for _, tt := range tests {
		got, err := Next(tt.from, tt.ev)
		if err != nil || got != tt.want {
			t.Errorf("Next(%s, %s) = %s, %v; want %s, nil", tt.from, tt.ev, got, err, tt.want)
		}
	}
}

func TestNextInvalid(t *testing.T) {
	tests := []struct {
		from State
		ev   Event
	}{
		{Requested, Granted}, // cannot skip approval and granting
		{PendingApproval, Granted},
		{Denied, Approve},     // terminal
		{Revoked, StartGrant}, // terminal
		{Active, Approve},
		{Approved, Granted}, // must go through Granting
		{Revoking, Granted}, // no resurrection while revoking
		{Active, RevokeDone},
	}
	for _, tt := range tests {
		got, err := Next(tt.from, tt.ev)
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("Next(%s, %s) = %s, %v; want ErrInvalidTransition", tt.from, tt.ev, got, err)
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []State{Denied, Revoked} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []State{Requested, PendingApproval, Approved, Granting, Active, Revoking} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
