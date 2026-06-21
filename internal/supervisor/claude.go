package supervisor

import "strconv"

// claudeArgs builds the argv for one supervised iteration.
//
// Iteration 1 starts a fresh print-mode session on the task prompt:
//
//	claude -p <task> --max-turns N [--model M] --dangerously-skip-permissions
//
// Later iterations resume the most recent session with the resume prompt:
//
//	claude -p --continue <resume> --max-turns N [--model M] --dangerously-skip-permissions
//
// --dangerously-skip-permissions is intentional: the agent runs fully
// autonomously inside a disposable sandbox VM, so interactive permission prompts
// would deadlock it (see DECISIONS D9).
func claudeArgs(bin string, iter int, task, resume, model string, maxTurns int) []string {
	if bin == "" {
		bin = "claude"
	}
	args := []string{bin, "-p"}
	if iter <= 1 {
		args = append(args, task)
	} else {
		args = append(args, "--continue", resume)
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "--dangerously-skip-permissions")
	return args
}
