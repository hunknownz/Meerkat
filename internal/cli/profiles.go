package cli

import "github.com/hunknownz/Meerkat/internal/core"

func cmdProfileList(env Env, args []string) (int, error) {
	fs, dd := newFlags("profile list")
	if err := parse(fs, args); err != nil {
		return ExitUsage, err
	}
	dir, err := dataDir(*dd)
	if err != nil {
		return ExitUsage, err
	}
	profiles, err := core.ListExecutionProfiles(dir)
	if err != nil {
		return ExitUsage, err
	}
	writeJSON(env.Stdout, map[string]any{"ok": true, "data": profiles})
	return ExitOK, nil
}
