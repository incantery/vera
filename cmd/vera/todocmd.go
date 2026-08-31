// `/todo`, and `vera todo` — the list, on the two screens the laptop
// has.
//
// `/tasks` is the fleet: agents Vera opened rooms for. This is the
// other list, the one that is yours — call the bank, book the flight
// — and the two are deliberately different words for that reason.
//
// Everything about what a line MEANS is decided in verad (see
// cmd/verad/todo.go), including which items a reference names and
// whether that was clear enough to act on. This file is only the
// screen: it prints what came back, and when what came back is a
// question it puts the candidates up as a card rather than making
// somebody type the number they were just shown.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/incantery/mote/tui"
	"github.com/incantery/vera/home"
)

// todoCommand is `/todo` in the chat. A change says one line; a list
// is a block, because a list is the thing you were asking to see.
func (s *chatSession) todoCommand(rest string) tea.Cmd {
	c := s.c
	return off(func(ctx context.Context) tea.Cmd {
		ans, err := c.todo(ctx, rest)
		if err != nil {
			return tui.Fail("todo: %s", err)
		}
		return s.showTodo(ans)
	})
}

func (s *chatSession) showTodo(ans *TodoAnswer) tea.Cmd {
	if ans.Question != "" {
		return s.todoQuestion(ans)
	}
	switch ans.Verb {
	case "list", "all":
		return tui.Show(home.TodoMarkdown(ans.Items, ans.Path, ans.Verb == "all"))
	}
	// A change, and then the count, so the line reads as an answer to
	// what was typed and still says where that leaves things.
	said := ans.Said
	if left := openCount(ans.Items); left != "" {
		said += " · " + left
	}
	return tui.Note("%s", said)
}

// todoQuestion puts the candidates up. Each row carries the exact
// line that picks it, so choosing is the same act as typing it — and
// cancelling leaves the list untouched, because nothing had happened
// yet when the question was asked.
func (s *chatSession) todoQuestion(ans *TodoAnswer) tea.Cmd {
	text := ans.Question
	if ans.Prose != "" {
		text += "\n" + ans.Prose
	}
	p := tui.Pick{Title: "todo", Text: text}
	lines := make([]string, 0, len(ans.Choices))
	for _, ch := range ans.Choices {
		p.Items = append(p.Items, tui.PickItem{Label: ch.Label, Detail: ch.Detail})
		lines = append(lines, ch.Line)
	}
	c := s.c
	p.OnPick = func(choice tui.PickChoice) tea.Cmd {
		if choice.Cancelled || choice.Item < 0 || choice.Item >= len(lines) {
			return nil
		}
		line := lines[choice.Item]
		return off(func(ctx context.Context) tea.Cmd {
			next, err := c.todo(ctx, line)
			if err != nil {
				return tui.Fail("todo: %s", err)
			}
			return s.showTodo(next)
		})
	}
	return tui.Choose(p)
}

// openCount is the tail of a one-line answer: what is left after the
// change that was just made.
func openCount(items []home.Item) string {
	n := len(home.Remaining(items))
	if n == 0 {
		if len(items) == 0 {
			return ""
		}
		return "nothing left"
	}
	return fmt.Sprintf("%d left", n)
}

// --- without a screen -----------------------------------------------------

const todoUsage = `vera todo                       what is left, one item a line
vera todo <something>           put it on the list
vera todo done <n|words>        cross one off (also: did, x)
vera todo undo <n|words>        put one back
vera todo drop <n|words>        take one off the list entirely
vera todo all | clear           show everything | sweep what is crossed off
`

// runTodo is the same call the chat makes, printed. It is here for
// scripts and for an agent driving Vera — including Vera's own tasks,
// which is why a question exits non-zero rather than picking for
// somebody: an agent that cannot see the card must not be handed a
// guess dressed as an answer.
func runTodo(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Print(todoUsage)
		return nil
	}
	base, err := ensure()
	if err != nil {
		return err
	}
	id, err := loadIdentity(identityPath())
	if err != nil {
		return err
	}
	c := &chatClient{base: base, secret: id.Secret, device: id.Name + " (cli)"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	line := strings.Join(args, " ")
	ans, err := c.todo(ctx, line)
	if err != nil {
		return err
	}
	if ans.Question != "" {
		fmt.Fprintln(os.Stderr, ans.Question)
		for _, ch := range ans.Choices {
			fmt.Fprintf(os.Stderr, "  vera todo %-12s %s\n", ch.Line, ch.Label)
		}
		os.Exit(1)
	}
	switch ans.Verb {
	case "list", "all":
		for _, it := range ans.Items {
			if it.Done && ans.Verb != "all" {
				continue
			}
			box := "[ ]"
			if it.Done {
				box = "[x]"
			}
			fmt.Printf("%2d %s %s\n", it.N, box, it.Text)
		}
		if len(ans.Items) == 0 {
			fmt.Println("nothing on the list")
		}
	default:
		fmt.Println(ans.Said)
	}
	return nil
}
