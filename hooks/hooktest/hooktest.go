// Package hooktest provides hook commands for tests, written for whichever
// shell atto runs hooks with: bash on Unix, PowerShell on Windows. Each
// function returns the bash script (as the tests always used) or an
// equivalent PowerShell one.
package hooktest

import (
	"strings"

	"github.com/sebastianrcnt/atto/shell"
)

func ps() bool { return shell.Default().Kind == shell.PowerShell }

// pick returns the bash or PowerShell variant.
func pick(bash, powershell string) string {
	if ps() {
		return powershell
	}
	return bash
}

// q single-quotes s for PowerShell.
func q(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

const readStdin = `[Console]::In.ReadToEnd()`

// BlockRmRf exits 2 with "no rm -rf" on stderr when stdin mentions rm -rf.
func BlockRmRf() string {
	return pick(`grep -q 'rm -rf' && { echo "no rm -rf" >&2; exit 2; } || exit 0`,
		`if (`+readStdin+`.Contains('rm -rf')) { [Console]::Error.WriteLine('no rm -rf'); exit 2 } else { exit 0 }`)
}

// EchoUpdatedInput allows the tool call and rewrites the bash command to "ls -la".
func EchoUpdatedInput() string {
	return `echo '{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"ls -la"}}}'`
}

// EchoAsk asks for approval with the reason "needs review".
func EchoAsk() string {
	return `echo '{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"needs review"}}'`
}

// Exit2 and Exit1 exit with that code.
func Exit2() string { return "exit 2" }
func Exit1() string { return "exit 1" }

// WriteStdinThenEcho overwrites path with stdin, then prints "remember: be brief".
func WriteStdinThenEcho(path string) string {
	return pick("cat > "+path+"; echo 'remember: be brief'",
		`[IO.File]::WriteAllText(`+q(path)+`, `+readStdin+`); echo 'remember: be brief'`)
}

// WriteStdinThenEchoText overwrites path with stdin, then prints text (no single quotes).
func WriteStdinThenEchoText(path, text string) string {
	return pick("cat > "+path+"; echo '"+text+"'",
		`[IO.File]::WriteAllText(`+q(path)+`, `+readStdin+`); echo '`+text+`'`)
}

// SpawnSleeper starts a child process that sleeps for 30 seconds while
// holding the hook's stdout open, then waits for it.
func SpawnSleeper() string {
	return pick("sleep 30 & wait",
		`Start-Process -NoNewWindow -Wait -FilePath powershell -ArgumentList '-NoProfile','-Command','Start-Sleep 30'`)
}

// OopsExit1 writes "oops" to stderr and exits 1.
func OopsExit1() string {
	return pick("echo oops >&2; exit 1", `[Console]::Error.WriteLine('oops'); exit 1`)
}

// DestructiveExit2 writes "destructive" to stderr and exits 2.
func DestructiveExit2() string {
	return pick(`echo "destructive" >&2; exit 2`, `[Console]::Error.WriteLine('destructive'); exit 2`)
}

// BlockOnFirstStop prints a block decision (reason "run the tests first")
// when stop_hook_active is false, and nothing otherwise.
func BlockOnFirstStop() string {
	return pick(`grep -q '"stop_hook_active":false' && echo '{"decision":"block","reason":"run the tests first"}' || true`,
		`if (`+readStdin+`.Contains('"stop_hook_active":false')) { '{"decision":"block","reason":"run the tests first"}' }`)
}

// LogStdin appends stdin and a newline to path.
func LogStdin(path string) string {
	return pick("cat >> "+path+"; echo >> "+path,
		`[IO.File]::AppendAllText(`+q(path)+`, `+readStdin+` + "`+"`n"+`")`)
}

// LogStdinThenBlock is LogStdin followed by a block decision with reason "again".
func LogStdinThenBlock(path string) string {
	return LogStdin(path) + `; echo '{"decision":"block","reason":"again"}'`
}

// LogStdinNoNewline appends stdin to path (the server test's log holds one entry).
func LogStdinNoNewline(path string) string {
	return pick("cat >> "+path,
		`[IO.File]::AppendAllText(`+q(path)+`, `+readStdin+`)`)
}

// BlockedByDecisionAndExit2 prints a block decision, writes "bye" to stderr
// and exits 2 (a SessionEnd hook cannot block).
func BlockedByDecisionAndExit2() string {
	return pick(`echo '{"decision":"block","reason":"no"}'; echo bye >&2; exit 2`,
		`echo '{"decision":"block","reason":"no"}'; [Console]::Error.WriteLine('bye'); exit 2`)
}

// Sleep30 sleeps for 30 seconds.
func Sleep30() string { return pick("sleep 30", "Start-Sleep 30") }

// StopOnce exits 0 when stop_hook_active is true, else writes "run the
// linter" (or msg) to stderr and exits 2.
func StopOnce(msg string) string {
	return pick(`grep -q '"stop_hook_active":true' && exit 0; echo "`+msg+`" >&2; exit 2`,
		`if (`+readStdin+`.Contains('"stop_hook_active":true')) { exit 0 }; [Console]::Error.WriteLine(`+q(msg)+`); exit 2`)
}
