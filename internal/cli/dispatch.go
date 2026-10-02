package cli

import "github.com/hunknownz/Meerkat/internal/server"

func cmdDispatch(env Env, args []string) (int, error) {
	fs, dd := newFlags("dispatch")
	var tasks multi
	fs.Var(&tasks, "task", "frozen task UUID (repeatable)")
	rid := fs.String("request-id", "", "required stable idempotency UUID")
	resume := fs.Bool("resume", false, "explicitly resume failed, stopped or acknowledged unknown tasks")
	ack := fs.Bool("acknowledge-interruption", false, "acknowledge after confirming interrupted processes are gone")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if len(tasks) < 1 || *rid == "" {
		return ExitUsage, usageErr{"--task and --request-id required"}
	}
	// Exit 0 acknowledges durable acceptance, not delivery. Query operation next.
	return remote(env, *dd, server.Request{Op: "dispatch", Tasks: tasks, RequestID: *rid, Resume: *resume, Acknowledge: *ack})
}

func cmdOperation(env Env, args []string) (int, error) {
	fs, dd := newFlags("operation")
	id := fs.String("operation", "", "operation UUID returned by dispatch")
	rid := fs.String("request-id", "", "recover a handle after a lost dispatch reply")
	wait := fs.Int("wait-ms", 0, "bounded wait 0..30000 milliseconds; timeout returns current state")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	if (*id == "") == (*rid == "") || *wait < 0 || *wait > 30000 || *rid != "" && *wait != 0 {
		return ExitUsage, usageErr{"use --operation or --request-id; request lookup cannot wait; --wait-ms must be 0..30000"}
	}
	if *rid != "" {
		return remote(env, *dd, server.Request{Op: "operation", RequestID: *rid})
	}
	return remote(env, *dd, server.Request{Op: "wait-operation", OperationID: *id, WaitMillis: *wait})
}
