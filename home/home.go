// Vera's home: what she knows, as files a person can read.
//
// Memory used to be one JSON array under ~/.local/state — a machine's
// format in a machine's directory, which meant the only way to see
// what Vera believed about you was to run a command that printed it
// back at you. A home is the other choice, and it is the same one
// Claude Code made: a directory of Markdown, one fact per file, that
// you can open, edit, grep, diff and put in git. The point is not the
// format. The point is that a memory you cannot see is a memory you
// cannot correct, and every wrong fact colours every answer after it.
//
// The layout:
//
//	~/vera/                  ($VERA_HOME overrides)
//	  MEMORY.md              the index — one line per memory, and the
//	                         part that goes into every prompt
//	  TODO.md                the list: what is not done yet, as boxes
//	  memory/<slug>.md       one fact per file, front matter then prose
//	  projects/<name>.md     what she knows about a repository
//	  notes/                 hers, to write in later
//	  profiles/supervisor/   the profile mote will define
//
// The files are the truth and the index is derived from them, so a
// file edited by hand wins and a hand-mangled index heals on the next
// write. It is 0700 throughout: this is a directory about a person.
package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The names, in one place, because both verad and `vera dump` walk
// this layout and they must agree about it.
const (
	Index       = "MEMORY.md"
	MemoryDir   = "memory"
	ProjectsDir = "projects"
	NotesDir    = "notes"
	ProfileDir  = "profiles/supervisor"
)

// Path is where home is: what was asked for, else $VERA_HOME, else
// ~/vera. Not under ~/.local/state, deliberately — a person is not
// going to go looking in a state directory for what a machine thinks
// of them.
func Path(override string) string {
	if p := strings.TrimSpace(override); p != "" {
		return expand(p)
	}
	if p := strings.TrimSpace(os.Getenv("VERA_HOME")); p != "" {
		return expand(p)
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "vera"
	}
	return filepath.Join(dir, "vera")
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if dir, err := os.UserHomeDir(); err == nil {
			return filepath.Join(dir, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// Home is the directory, opened.
type Home struct {
	Root string

	// One writer per process. Extraction runs on its own goroutine
	// behind every reply and the fleet writes project files from the
	// supervisor's, so two writers is the normal case, not the exotic
	// one. Everything that touches the directory takes this.
	mu  sync.Mutex
	mem *Memory
}

// Open makes the layout if it is not there, and reads what is in it.
func Open(root string) (*Home, error) {
	h := &Home{Root: root}
	if err := h.ensure(); err != nil {
		return nil, err
	}
	h.mem = &Memory{home: h, limit: 120, promptCap: 6 << 10}
	if err := h.mem.reload(); err != nil {
		return nil, err
	}
	return h, nil
}

// Memory is what she knows about the person.
func (h *Home) Memory() *Memory { return h.mem }

func (h *Home) path(parts ...string) string {
	return filepath.Join(append([]string{h.Root}, parts...)...)
}

func (h *Home) ensure() error {
	for _, dir := range []string{"", MemoryDir, ProjectsDir, NotesDir, ProfileDir} {
		if err := os.MkdirAll(h.path(filepath.FromSlash(dir)), 0o700); err != nil {
			return err
		}
	}
	// The index is created empty. It goes into the prompt whole, so
	// anything written here as a greeting would be something the model
	// reads as a thing it knows about the person, on every exchange.
	if err := writeIfMissing(h.path(Index), ""); err != nil {
		return err
	}
	// The README used to say this directory was empty on purpose and
	// that nothing read it. It is read now, so an old home gets the
	// new words rather than a stale note beside a live profile.
	if err := writeIfStale(h.path(filepath.FromSlash(ProfileDir), "README.md"), supervisorReadme, "Empty on purpose."); err != nil {
		return err
	}
	// The list is seeded with its own explanation, unlike the index:
	// nothing reads TODO.md into a prompt, so words at the top of it
	// are words for the person and cost nothing.
	if err := writeIfMissing(h.path(Todo), todoPreamble); err != nil {
		return err
	}
	return writeIfMissing(h.path(NotesDir, "README.md"), notesReadme)
}

const supervisorReadme = `# supervisor

Vera's profile, and the only place her rules live.

  profile.md   what she is, in her own prompt
  policy.toml  what her tools may touch: allow, ask or deny, by tool,
               by path, by command prefix

verad reads both at startup and writes mote's worked example here the
first time, if there is nothing. After that these are yours: edit them
and restart verad.

Two things are not in the file, because the file cannot know them.
The repositories ` + "`${root}`" + ` stands for are the ones the fleet knows
about, added to the list here. And this directory is denied to her
tools whatever the rules say — a rule she can rewrite is not a rule.
`

const notesReadme = `# notes

Hers. Nothing reads this directory yet — it is where Vera writes what
she is working out, once she has the tools to write at all.
`

// writeIfStale replaces a file this package wrote earlier, when it
// still begins the way the old version did and nobody has made it
// theirs. A file a person has edited is left alone.
func writeIfStale(path, content, was string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return write(path, content)
		}
		return err
	}
	if !strings.Contains(string(b), was) {
		return nil
	}
	return write(path, content)
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return write(path, content)
}

// write puts a file down through a temporary and a rename, so a crash
// halfway leaves the old file rather than half of the new one.
func write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Describe is the line for verad's banner: where home is and how much
// is in it.
func (h *Home) Describe() string {
	n := h.mem.Count()
	things := "things"
	if n == 1 {
		things = "thing"
	}
	return fmt.Sprintf("%s (%d %s remembered)", h.Root, n, things)
}
