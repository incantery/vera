// Command vera is the front door. Bare `vera` makes sure verad is
// running and opens the chat; the verbs manage the daemon. It mirrors
// rook/rookd: one command people type, one process that stays up.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

const usage = `vera — talk to her, and keep her running

  vera                    chat (starts verad if it is not running)
  vera chat [-c id]       chat, reopening a conversation
  vera start [verad flags] start verad detached, if it is not running
  vera stop               stop verad
  vera restart [flags]    stop, then start
  vera status             is verad up, where, since when
  vera log [-f]           verad's log
  vera url                the pairing page
  vera home               where her memory lives (MEMORY.md, memory/, projects/)
  vera say [-c id] [-m model] [-e effort] <text>
                          one exchange, reply on stdout — for scripts and other agents
  vera costs [--since 7d] [--by model|conversation|day]
                          what the journal says every exchange cost
  vera tasks              every task and what is believed about it
  vera task <verb> ...    start | answer | report | land | stop | resume | seen — the fleet, by hand
  vera sessions           the conversations the chat left behind
  vera mcp                the MCP servers she can reach, and their tools
  vera dump [ids...]      a folder of everything about a conversation, to report a problem
  vera install            a launchd agent so verad starts at login
  vera uninstall          remove it
  vera version
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runChat(nil)
		return
	}
	var err error
	switch args[0] {
	case "chat":
		runChat(args[1:])
	case "start":
		err = start(args[1:], true)
	case "stop":
		err = stop()
	case "restart":
		if err = stop(); err == nil {
			err = start(args[1:], true)
		}
	case "status":
		err = status()
	case "log":
		err = showLog(len(args) > 1 && args[1] == "-f")
	case "url":
		err = showURL()
	case "home":
		err = showHome()
	case "sessions":
		err = runSessions(args[1:])
	case "mcp":
		err = runMCP(args[1:])
	case "dump":
		err = runDump(args[1:])
	case "say":
		err = runSay(args[1:])
	case "costs":
		err = runCosts(args[1:])
	case "tasks":
		err = runFleet("tasks", nil)
	case "task":
		if len(args) < 2 {
			fmt.Fprint(os.Stderr, fleetUsage)
			os.Exit(1)
		}
		err = runFleet(args[1], args[2:])
	case "install":
		err = install()
	case "uninstall":
		err = uninstall()
	case "version", "--version", "-v":
		fmt.Println("vera " + version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "vera: unknown command %q\n%s", args[0], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vera:", err)
		os.Exit(1)
	}
}
