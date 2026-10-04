package executor

import "errors"

// RunControl carries a validated bounded instruction, never an executor command. The authority
// must persist the send intention before the adapter performs any protocol write.
type RunControl struct{ ID, Kind, Message string }
type ControlAuthority interface {
	Begin(string) error
	Finish(id, state, disposition, reason string) error
}
type ControlBinding struct {
	Messages  <-chan RunControl
	Authority ControlAuthority
}

var ErrControlRejected = errors.New("control definitely rejected before send")

// ControlDrainAuthority seals admission after accepted sends have drained.
// The adapter verifies its protocol queue before and after this handshake.
type ControlDrainAuthority interface{ Quiesce() (bool, error) }
