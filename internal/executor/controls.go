package executor

// RunControl carries no prompt text or backend-specific command. The authority
// must persist the send intention before the adapter performs any protocol write.
type RunControl struct{ ID, Kind string }
type ControlAuthority interface {
	Begin(string) error
	Finish(id, state, disposition, reason string) error
}
type ControlBinding struct {
	Messages  <-chan RunControl
	Authority ControlAuthority
}
