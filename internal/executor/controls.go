package executor

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
