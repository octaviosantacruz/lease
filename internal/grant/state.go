// Package grant holds the grant lifecycle as a pure state machine.
package grant

import "errors"

// State is the lifecycle position of a grant.
type State string

const (
	Requested       State = "requested"
	PendingApproval State = "pending_approval"
	Approved        State = "approved"
	Denied          State = "denied"
	Granting        State = "granting"
	Active          State = "active"
	Revoking        State = "revoking"
	Revoked         State = "revoked"
)

// Event moves a grant from one state to another.
type Event string

const (
	NeedsApproval Event = "needs_approval"
	AutoApprove   Event = "auto_approve"
	Approve       Event = "approve"
	Deny          Event = "deny"
	StartGrant    Event = "start_grant"
	Granted       Event = "granted"
	Fail          Event = "fail"
	Expire        Event = "expire"
	Revoke        Event = "revoke"
	RevokeDone    Event = "revoked"
)

// ErrInvalidTransition is returned for any (state, event) pair not in the table.
var ErrInvalidTransition = errors.New("grant: invalid transition")

// Terminal reports whether no further event is accepted.
func (s State) Terminal() bool {
	return s == Denied || s == Revoked
}

// Next returns the state reached from `from` on `ev`, or ErrInvalidTransition.
//
// Una struct comparable sirve como clave: la tabla enumera sólo lo permitido.
func Next(from State, ev Event) (State, error) {
	next, ok := transitions[transition{from, ev}]
	if !ok {
		return from, ErrInvalidTransition
	}
	return next, nil
}

type transition struct {
	from  State
	event Event
}

var transitions = map[transition]State{
	{Requested, NeedsApproval}: PendingApproval,
	{Requested, AutoApprove}:   Approved,
	{Requested, Deny}:          Denied,
	{Requested, Revoke}:        Revoking, // intento de limpieza de drift observado
	{PendingApproval, Approve}: Approved,
	{PendingApproval, Deny}:    Denied,
	{Approved, StartGrant}:     Granting,
	{Approved, Deny}:           Denied, // la ventana puede vencer antes de aplicar el permiso
	{Granting, Granted}:        Active,
	{Granting, Fail}:           Revoking,
	{Active, Expire}:           Revoking,
	{Active, Revoke}:           Revoking,
	{Revoking, RevokeDone}:     Revoked,
	{Revoking, Fail}:           Revoking, // cada intento fallido conserva evidencia y sigue pendiente
}
