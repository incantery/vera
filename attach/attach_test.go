package attach

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pngBytes is a real PNG, because the store sniffs rather than
// believes: a test that handed it "PNG" in a MIME field would prove
// nothing about what it does with a screenshot.
func pngBytes(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{shade, shade, shade, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func gifBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	if err := gif.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func store(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: t.TempDir()}
}

func TestSavesBase64AndAnswersWithAPath(t *testing.T) {
	s := store(t)
	raw := pngBytes(t, 7)
	saved, err := s.Save("chat-1", []Image{{Name: "Screenshot.png", Data: base64.StdEncoding.EncodeToString(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved %d", len(saved))
	}
	if saved[0].MIME != "image/png" || saved[0].Bytes != len(raw) {
		t.Fatalf("saved %+v", saved[0])
	}
	if saved[0].Name != "Screenshot.png" {
		t.Fatalf("lost the person's name for it: %q", saved[0].Name)
	}
	on, err := os.ReadFile(saved[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(on, raw) {
		t.Fatal("the file on disk is not the picture that arrived")
	}
	// Under the conversation, so one task's evidence is not mixed
	// with another's.
	if filepath.Base(filepath.Dir(saved[0].Path)) != "chat-1" {
		t.Fatalf("kept at %s", saved[0].Path)
	}
	if !strings.HasSuffix(saved[0].Path, ".png") {
		t.Fatalf("no extension: %s", saved[0].Path)
	}
}

// A pasteboard and a browser both hand out `data:` URIs; no caller
// should have to know that.
func TestSavesADataURI(t *testing.T) {
	s := store(t)
	raw := pngBytes(t, 9)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	saved, err := s.Save("c", []Image{{Data: uri}})
	if err != nil {
		t.Fatal(err)
	}
	on, _ := os.ReadFile(saved[0].Path)
	if !bytes.Equal(on, raw) {
		t.Fatal("the data: URI did not survive")
	}
}

// base64 that travelled through a text field arrives with newlines in
// it, and a sender that dropped the padding is not a sender to refuse.
func TestSavesWrappedAndUnpaddedBase64(t *testing.T) {
	s := store(t)
	raw := pngBytes(t, 11)
	wrapped := base64.StdEncoding.EncodeToString(raw)
	var lines []string
	for len(wrapped) > 40 {
		lines, wrapped = append(lines, wrapped[:40]), wrapped[40:]
	}
	lines = append(lines, wrapped)
	if _, err := s.Save("c", []Image{{Data: strings.Join(lines, "\n")}}); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if _, err := s.Save("c", []Image{{Data: base64.RawStdEncoding.EncodeToString(raw)}}); err != nil {
		t.Fatalf("unpadded: %v", err)
	}
}

// Read is how a caller on this machine turns a file into a message:
// the CLI's -i, the chat's /image and /paste. What is kept is a copy,
// so it outlives whatever temporary file a screenshot tool wrote.
func TestReadTurnsAFileIntoAMessageAndTheCopyOutlivesIt(t *testing.T) {
	s := store(t)
	raw := pngBytes(t, 13)
	src := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	im, err := Read(src)
	if err != nil {
		t.Fatal(err)
	}
	if im.Name != "shot.png" {
		t.Fatalf("lost the name: %q", im.Name)
	}
	saved, err := s.Save("c", []Image{im})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].Path == src {
		t.Fatal("kept the caller's path instead of copying")
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	on, err := os.ReadFile(saved[0].Path)
	if err != nil || !bytes.Equal(on, raw) {
		t.Fatalf("the copy did not survive the original: %v", err)
	}

	// A file that is not there, one that is too big, and one that is
	// not a picture at all are the caller's to hear about before
	// anything is encoded and before an exchange is paid for.
	if _, err := Read(filepath.Join(t.TempDir(), "gone.png")); err == nil {
		t.Error("read a file that is not there")
	}
	prose := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(prose, []byte("this is just some prose"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(prose); err == nil || !strings.Contains(err.Error(), "notes.txt") {
		t.Errorf("read something that is not a picture: %v", err)
	}
	huge := filepath.Join(t.TempDir(), "huge.png")
	if err := os.WriteFile(huge, bytes.Repeat([]byte{0}, MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(huge); err == nil {
		t.Error("read a file over the ceiling")
	}
}

// The same screenshot pasted twice is one file: the name is its
// contents.
func TestTheSamePictureIsKeptOnce(t *testing.T) {
	s := store(t)
	raw := pngBytes(t, 17)
	one, err := s.Save("c", []Image{{Data: base64.StdEncoding.EncodeToString(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Save("c", []Image{{Data: base64.StdEncoding.EncodeToString(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	if one[0].Path != two[0].Path {
		t.Fatalf("%s != %s", one[0].Path, two[0].Path)
	}
	entries, _ := os.ReadDir(filepath.Dir(one[0].Path))
	if len(entries) != 1 {
		t.Fatalf("%d files for one picture", len(entries))
	}
}

func TestKeepsTheOtherFormats(t *testing.T) {
	s := store(t)
	saved, err := s.Save("c", []Image{{Data: base64.StdEncoding.EncodeToString(gifBytes(t))}})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].MIME != "image/gif" || !strings.HasSuffix(saved[0].Path, ".gif") {
		t.Fatalf("saved %+v", saved[0])
	}
}

// Sniffed, not believed. A caller that says PNG and sends a PDF is
// refused — the alternative is an agent handed a file it cannot read
// and a person told the work is under way.
func TestRefusesWhatIsNotAPicture(t *testing.T) {
	s := store(t)
	_, err := s.Save("c", []Image{{Name: "notes.txt", MIME: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("this is just some prose"))}})
	if err == nil {
		t.Fatal("kept it")
	}
	// Named, so the person knows which one to fix.
	if !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("unhelpful: %v", err)
	}
}

func TestRefusesTooManyAndTooBig(t *testing.T) {
	s := store(t)
	many := make([]Image, MaxImages+1)
	for i := range many {
		many[i] = Image{Data: base64.StdEncoding.EncodeToString(pngBytes(t, uint8(i)))}
	}
	if _, err := s.Save("c", many); err == nil {
		t.Fatal("took more than it will carry")
	}
	over := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, MaxBytes+1))
	if _, err := s.Save("c", []Image{{Data: over}}); err == nil {
		t.Fatal("took an image over the ceiling")
	}
}

func TestRefusesNonsense(t *testing.T) {
	s := store(t)
	for name, im := range map[string]Image{
		"nothing at all": {Name: "x"},
		"not base64":     {Data: "!!!! not base64 !!!!"},
		"empty":          {Data: base64.StdEncoding.EncodeToString(nil)},
	} {
		if _, err := s.Save("c", []Image{im}); err == nil {
			t.Errorf("%s: took it", name)
		}
	}
}

// Nothing in, nothing out, no directory made: the text-only path stays
// exactly what it was.
func TestNoImagesIsNotAnError(t *testing.T) {
	s := store(t)
	saved, err := s.Save("c", nil)
	if err != nil || saved != nil {
		t.Fatalf("%v %v", saved, err)
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 0 {
		t.Fatalf("made %d directories for no pictures", len(entries))
	}
}

// A Vera with nowhere to keep pictures says so rather than panicking
// or silently dropping the evidence.
func TestAStoreWithNoDirectoryRefuses(t *testing.T) {
	var s *Store
	if _, err := s.Save("c", []Image{{Data: "x"}}); err != ErrNoStore {
		t.Fatalf("nil store: %v", err)
	}
	if _, err := (&Store{}).Save("c", []Image{{Data: "x"}}); err != ErrNoStore {
		t.Fatalf("empty dir: %v", err)
	}
}

// A conversation id comes off the wire. It names a directory, so it
// must not be able to name one outside the store.
func TestAConversationIdCannotWalkOutOfTheStore(t *testing.T) {
	s := store(t)
	saved, err := s.Save("../../etc/nope", []Image{{Data: base64.StdEncoding.EncodeToString(pngBytes(t, 3))}})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(s.Dir, saved[0].Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("escaped to %s", saved[0].Path)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"chat-20260829-101112": "chat-20260829-101112",
		"":                     "loose",
		"../../etc":            "etc",
		"a/b":                  "a-b",
		"...":                  "loose",
	} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Old conversations go; the one being written to does not.
func TestSweepsOldConversations(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Keep: time.Hour}
	old := filepath.Join(s.Dir, "ancient")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	long := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, long, long); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Save("today", []Image{{Data: base64.StdEncoding.EncodeToString(pngBytes(t, 5))}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("kept a month-old conversation's pictures")
	}
	if _, err := os.Stat(saved[0].Path); err != nil {
		t.Fatal("swept the picture it was saving")
	}
}

// --- what the words say ---------------------------------------------------

func TestNoteSaysSheCannotSeeItAndWhoCan(t *testing.T) {
	if Note(nil) != "" {
		t.Fatal("a message with no picture must read exactly as it did before")
	}
	one := Note([]Saved{{Path: "/s/a.png", Name: "shot.png"}})
	for _, want := range []string{"/s/a.png", "shot.png", "cannot see", "delegate", "fleet"} {
		if !strings.Contains(one, want) {
			t.Errorf("note is missing %q: %s", want, one)
		}
	}
	two := Note([]Saved{{Path: "/s/a.png"}, {Path: "/s/b.png"}})
	if !strings.Contains(two, "2 images") || !strings.Contains(two, "/s/b.png") {
		t.Errorf("two images: %s", two)
	}
}

func TestBriefNamesTheFilesAndTellsTheAgentToOpenThem(t *testing.T) {
	if got := Brief("fix the layout", nil); got != "fix the layout" {
		t.Fatalf("a task with no picture must be untouched: %q", got)
	}
	got := Brief("fix the layout", []string{"/s/a.png", "/s/b.png"})
	if !strings.HasPrefix(got, "fix the layout") {
		t.Fatal("the goal must still be the first thing the agent reads")
	}
	for _, want := range []string{"2 images", "/s/a.png", "/s/b.png", "Read them", "do not commit"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief is missing %q:\n%s", want, got)
		}
	}
}

func TestPathsAndSummary(t *testing.T) {
	saved := []Saved{{Path: "/s/a.png", Bytes: 900 << 10}, {Path: "/s/b.png", Bytes: 200 << 10}}
	got := Paths(saved)
	if len(got) != 2 || got[0] != "/s/a.png" || got[1] != "/s/b.png" {
		t.Fatalf("paths %v", got)
	}
	if Paths(nil) != nil || Summary(nil) != "" {
		t.Fatal("nothing in, nothing out")
	}
	if s := Summary(saved); s != "2 images (1.1 MB)" {
		t.Fatalf("summary %q", s)
	}
	if s := Summary(saved[1:]); s != "1 image (200 KB)" {
		t.Fatalf("summary %q", s)
	}
}

// An answer is typed into a pane, and a newline typed into a terminal
// is a Return that sends half of it. So the one-line form has no line
// breaks of its own, whatever else it says.
func TestLineHasNoLineBreaks(t *testing.T) {
	if got := Line("just words", nil); got != "just words" {
		t.Fatalf("no pictures must change nothing: %q", got)
	}
	got := Line("here is what I see", []string{"/s/a.png", "/s/b.png"})
	if strings.Contains(got, "\n") {
		t.Fatalf("a newline would send half of it: %q", got)
	}
	for _, want := range []string{"here is what I see", "/s/a.png", "/s/b.png", "image files", "open them"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	// A picture with nothing said is still a whole message.
	alone := Line("", []string{"/s/a.png"})
	if strings.HasPrefix(alone, " ") || !strings.Contains(alone, "image file,") {
		t.Errorf("a picture on its own reads as %q", alone)
	}
}
