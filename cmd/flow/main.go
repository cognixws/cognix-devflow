// Command flow is the Dev flow engine: the development flow of COGNIX[WS]
// bound to any stack by the project's contract in .picode.json. It runs
// inside COGNIX[WS] (the extension's tools call it) and on its own, in CI or
// for anyone without COGNIX[WS].
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

var version = "0.1.0-dev"

const usage = `flow — the Dev flow engine

  flow status   [--json]                 where this checkout stands and what is next
  flow check    [--full] [--only name] [--fresh] [--json]
                                         run the checks this change needs
  flow close    [--strict] [--json]      end of a change: checks, process rules, summary
  flow validate [--json]                 the process rules only
  flow scope    [--contract file] [--batch] < paths
                                         the checks a list of paths selects
  flow debt     --topic t [--pay [--note n]] <text>
                                         add a debt to a topic file, or mark one paid
  flow adr      [--title t] <name>       seed a decision record with the next free number
  flow guide    [topic]                  print a craft guide (quality-gate, handoff-note, …)
  flow land     [--json]                 run the project's own land command (contract "land")
  flow hooks    on|off|check [--json]   git protection in this clone (core.hooksPath)
  flow agents   [--write [--force]] [--json]
                                         the Dev flow block in AGENTS.md: report, or put the current one in place
  flow serve    [--port p] [--data d]    the background process COGNIX[WS] runs
  flow tools-exec                        one agent tool call (picode-tools/1 on stdin)
  flow version

The project's contract is extensions.picode-devflow in .picode.json.
`

// exitError carries an exit code without printing a second message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "status":
		err = cmdStatus(args[1:], stdout)
	case "check":
		err = cmdCheck(args[1:], stdout)
	case "close":
		err = cmdClose(args[1:], stdout)
	case "validate":
		err = cmdValidate(args[1:], stdout)
	case "scope":
		err = cmdScope(args[1:], stdin, stdout)
	case "debt":
		err = cmdDebt(args[1:], stdout)
	case "adr":
		err = cmdADR(args[1:], stdout)
	case "guide":
		err = cmdGuide(args[1:], stdout)
	case "land":
		err = cmdLand(args[1:], stdout)
	case "serve":
		err = cmdServe(args[1:], stdout)
	case "tools-exec":
		err = cmdToolsExec(stdin, stdout)
	case "hooks":
		err = cmdHooks(args[1:], stdout)
	case "agents":
		err = cmdAgents(args[1:], stdout)
	case "hook":
		err = cmdHook(args[1:], stdin, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "flow", version)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "flow: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil {
		fmt.Fprintln(stderr, "flow:", err)
		return 1
	}
	return 0
}
