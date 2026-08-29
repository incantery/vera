// Package attach is the pictures a person hands Vera.
//
// A screenshot is the cheapest sentence there is: "this, here, look".
// Saying it in prose costs a paragraph and loses the part that
// mattered. So the door Vera answers has to take one.
//
// What arrives is bytes on a wire; what Vera passes on is a PATH. That
// asymmetry is the whole design, and it is not laziness:
//
//   - Vera's own model is reached through mote's provider, whose
//     Message is text and nothing else. She cannot look at a picture
//     today, and making her able to is a change to mote rather than
//     to Vera. See "Pictures" in cmd/verad/README.md.
//   - The agents she hands work to — Claude Code, as a delegate or in
//     a fleet room — read images from disk perfectly well. Handing
//     over a file path is the whole of what they need, and it costs no
//     tokens until the agent decides to open it.
//
// So an image is kept once, under Vera's own state, and travels as a
// path from there. The person's message says a screenshot came with
// it; the delegate and the fleet are handed the file. Nothing about
// the text-only path changes: no images, no note, no new bytes.
package attach

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Image is one picture on the wire, as a phone, a Mac panel or the CLI
// sends it.
//
// Bytes and nothing else. A path would have been cheaper for a caller
// on this machine, and it is left out on purpose: it would be a field
// meaning "read whatever file I name", arriving over a network, from a
// device, to be handed to an agent with a shell. Read() is how a
// local caller turns a file into one of these, on its own side of the
// wire, where the file is already its to read.
type Image struct {
	// Name is what the person called it — "Screenshot 2026-08-29.png".
	// Decoration: the file Vera writes is named after its contents.
	Name string `json:"name,omitempty"`
	// MIME is what the sender believes it is. It is not trusted; the
	// bytes are sniffed either way, and a disagreement is the bytes'.
	MIME string `json:"mime,omitempty"`
	// Data is the picture itself: base64, with or without a `data:`
	// URI wrapper around it.
	Data string `json:"data,omitempty"`
}

// Read turns a file on this machine into an Image ready to send —
// what `vera say -i` and the chat's /image and /paste do.
//
// The ceiling is checked here rather than after base64 has grown it by
// a third: a caller that pointed at a movie should be told so before
// anything is encoded.
func Read(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return Image{}, err
	}
	if _, err := checked(raw); err != nil {
		return Image{}, err
	}
	// Sniffed here as well as in Save. A person who typed the wrong
	// file name should hear about it now — before an exchange has been
	// paid for — rather than as a complaint that comes back with the
	// answer.
	kind, ok := kindOf(raw)
	if !ok {
		return Image{}, fmt.Errorf("%s is %s, not a picture Vera keeps — send a PNG, JPEG, GIF or WebP",
			filepath.Base(path), kind)
	}
	return Image{Name: filepath.Base(path), MIME: kind, Data: base64.StdEncoding.EncodeToString(raw)}, nil
}

// kindOf is what the bytes are, and whether it is one of the four.
// Sniffed rather than believed: a caller that says PNG and sends a PDF
// would otherwise hand an agent a file it cannot read.
func kindOf(raw []byte) (string, bool) {
	// The sniffer answers a Content-Type, which may carry parameters.
	kind, _, _ := strings.Cut(http.DetectContentType(raw), ";")
	kind = strings.TrimSpace(kind)
	_, ok := kinds[kind]
	return kind, ok
}

// The ceilings. Generous enough for a Retina screenshot of a whole
// display, small enough that a paired device cannot fill a disk with
// one request.
const (
	// MaxBytes is one image, decoded.
	MaxBytes = 16 << 20
	// MaxImages is how many may ride on one message.
	MaxImages = 8
)

// kinds are the formats Vera keeps, by what the sniffer calls them.
// Deliberately short: these are the four every screenshot tool and
// every phone can produce, and every one of them is a format Claude
// Code will open. Anything else is refused by name rather than saved
// and quietly ignored downstream.
var kinds = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// ErrNoStore is images arriving somewhere with nowhere to put them.
var ErrNoStore = errors.New("this Vera has nowhere to keep a picture")

// Saved is one picture on disk, as everything downstream sees it.
type Saved struct {
	// Path is the file. It is absolute, and it is what an agent is
	// handed.
	Path string `json:"path"`
	// Name is the person's name for it, when they had one.
	Name string `json:"name,omitempty"`
	MIME string `json:"mime"`
	// Bytes is the size on disk, for a screen that wants to say so.
	Bytes int `json:"bytes"`
}

// Store is where pictures live: one directory per conversation under
// Vera's own state, so that what a task is still reading is not mixed
// up with what somebody said last week.
type Store struct {
	// Dir is the root. Empty is a store that refuses.
	Dir string
	// Keep is how long a conversation's pictures survive. Zero means
	// DefaultKeep. A fleet task started this morning may not open its
	// screenshot until this afternoon, so this is long rather than
	// tidy.
	Keep time.Duration
}

// DefaultKeep is a month: far longer than any task, short enough that
// a year of screenshots is not still on the disk.
const DefaultKeep = 30 * 24 * time.Hour

// Save writes every image and answers with where they went.
//
// It is all-or-nothing on validity: a message carrying one thing that
// is not an image is a mistake worth reporting, not a message to
// answer with half its evidence. Files already written are left —
// they are content-addressed, so a retry lands on the same names.
func (s *Store) Save(conversation string, images []Image) ([]Saved, error) {
	if len(images) == 0 {
		return nil, nil
	}
	if s == nil || strings.TrimSpace(s.Dir) == "" {
		return nil, ErrNoStore
	}
	if len(images) > MaxImages {
		return nil, fmt.Errorf("that is %d images; %d is the most that can ride on one message", len(images), MaxImages)
	}
	dir := filepath.Join(s.Dir, slug(conversation))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.sweep()

	out := make([]Saved, 0, len(images))
	for i, im := range images {
		raw, err := im.bytes()
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i+1, err)
		}
		kind, ok := kindOf(raw)
		if !ok {
			return nil, fmt.Errorf("image %d (%s) is %s, which Vera does not keep — send a PNG, JPEG, GIF or WebP",
				i+1, describeName(im.Name), kind)
		}
		ext := kinds[kind]
		sum := sha256.Sum256(raw)
		path := filepath.Join(dir, hex.EncodeToString(sum[:])[:16]+ext)
		// Content-addressed, so the same screenshot pasted twice is
		// one file and the second write is skipped.
		if st, err := os.Stat(path); err != nil || st.Size() != int64(len(raw)) {
			if err := write(path, raw); err != nil {
				return nil, fmt.Errorf("image %d: %w", i+1, err)
			}
		}
		out = append(out, Saved{Path: path, Name: strings.TrimSpace(im.Name), MIME: kind, Bytes: len(raw)})
	}
	return out, nil
}

// bytes is the picture itself, decoded.
func (im Image) bytes() ([]byte, error) {
	data := strings.TrimSpace(im.Data)
	if data == "" {
		return nil, errors.New("an image with no bytes in it is nothing")
	}
	// A `data:` URI is what a pasteboard and a browser both hand out,
	// and stripping it here means no caller has to know.
	if strings.HasPrefix(data, "data:") {
		_, rest, ok := strings.Cut(data, ",")
		if !ok {
			return nil, errors.New("that data: URI has no comma in it")
		}
		data = rest
	}
	// Whitespace is what a base64 that travelled through a text field
	// looks like. RawStdEncoding covers a sender that dropped the
	// padding.
	data = strings.Join(strings.Fields(data), "")
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		if raw, err = base64.RawStdEncoding.DecodeString(data); err != nil {
			return nil, errors.New("the image data is not base64")
		}
	}
	return checked(raw)
}

func checked(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("that image is empty")
	}
	if len(raw) > MaxBytes {
		return nil, fmt.Errorf("that image is over %d MB, which is more than Vera will carry", MaxBytes>>20)
	}
	return raw, nil
}

// write puts the file down whole or not at all: an agent that opens a
// half-written screenshot gets a decoder error instead of a picture.
func write(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".attach-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// sweep drops conversations nobody has added to in a long time. Best
// effort and silent: failing to tidy is not a reason to refuse a
// picture, and there is nobody to tell.
func (s *Store) sweep() {
	keep := s.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-keep)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.Dir, e.Name()))
	}
}

// Paths is the files, in order — what a tool is handed.
func Paths(saved []Saved) []string {
	if len(saved) == 0 {
		return nil
	}
	out := make([]string, 0, len(saved))
	for _, s := range saved {
		out = append(out, s.Path)
	}
	return out
}

// Note is what is added to the person's own words before the model
// reads them.
//
// It says three things, and each is load-bearing. That a picture came
// with the message — otherwise "what's wrong with this?" is a question
// about nothing. That she cannot see it herself — otherwise she
// answers as if she had. And that whatever she hands on CAN see it —
// which is what turns "I can't look at that" into a delegation.
func Note(saved []Saved) string {
	if len(saved) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[")
	if len(saved) == 1 {
		b.WriteString("They attached an image to this message. It is a file on this machine: " +
			saved[0].describe() + ". You cannot see it yourself — you have no eyes. Claude Code can: ")
	} else {
		fmt.Fprintf(&b, "They attached %d images to this message. They are files on this machine:", len(saved))
		for _, s := range saved {
			b.WriteString("\n  " + s.describe())
		}
		b.WriteString("\nYou cannot see them yourself — you have no eyes. Claude Code can: ")
	}
	b.WriteString("any task you hand to the delegate or the fleet is given these files " +
		"automatically, and the agent there opens them. So if the answer depends on what is in " +
		"the picture, hand the work on rather than guessing or apologising, and do not repeat the " +
		"file path back to them.]")
	return b.String()
}

// Trouble is what the model is told when a picture arrived and could
// not be kept.
//
// It goes in the TURN rather than out as an error frame, and that is a
// decision about clients rather than about taste: an error frame is
// treated as terminal by two of the four things that read this wire —
// the phone breaks its read loop on one, the Mac panel throws — so a
// refusal sent that way would truncate the answer it was trying to
// annotate. The turn reaches every client, because the turn is the
// answer.
//
// The one behaviour worth ruling out is answering as though nothing
// was attached. Somebody who pasted a screenshot and asked "what is
// wrong here?" would get a reply about nothing, with no way to tell
// why.
func Trouble(err error) string {
	if err == nil {
		return ""
	}
	return "\n\n[They attached an image to this message and it could not be kept: " +
		err.Error() + ". You do not have it. Say so in a sentence — what went wrong, and that " +
		"they can send it again — and do not answer as though you had seen it.]"
}

func (s Saved) describe() string {
	if s.Name != "" {
		return s.Path + " (" + s.Name + ")"
	}
	return s.Path
}

// Brief is a task with the pictures named in it — what the delegate
// and the fleet actually hand to Claude Code.
//
// Appended rather than prefixed: the goal is the first thing the agent
// reads, and the evidence follows it. It is written as an instruction
// because an agent that is merely told a file exists frequently does
// not open it.
func Brief(task string, paths []string) string {
	if len(paths) == 0 {
		return task
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(task, "\n"))
	b.WriteString("\n\n")
	if len(paths) == 1 {
		b.WriteString("The person attached an image to this request — a screenshot or a photo. Read it before you start:\n\n")
	} else {
		fmt.Fprintf(&b, "The person attached %d images to this request — screenshots or photos. Read them before you start:\n\n", len(paths))
	}
	for _, p := range paths {
		b.WriteString("  " + p + "\n")
	}
	b.WriteString("\nThey are ordinary image files; open them with your file-reading tool, which renders images. " +
		"They live in Vera's own state directory rather than in the working tree — read them where they are, " +
		"do not copy or move them, and do not commit them.")
	return b.String()
}

// Line is text with its pictures named on ONE line, for somewhere the
// words are TYPED rather than handed over — a terminal pane.
//
// A newline typed into a pane is a Return, and a Return in the middle
// of an answer sends half of it and leaves the rest as a second
// message. Brief's paragraph is right for a subprocess argument and
// wrong here, so this is the same information with no line breaks of
// its own.
func Line(text string, paths []string) string {
	if len(paths) == 0 {
		return text
	}
	said := strings.TrimSpace(text)
	if said != "" {
		said += " "
	}
	noun := "image file"
	if len(paths) > 1 {
		noun = "image files"
	}
	return said + "[attached: " + strings.Join(paths, ", ") + " — " + noun + ", open them]"
}

// Summary is one short line for a screen: "2 images (1.4 MB)".
func Summary(saved []Saved) string {
	if len(saved) == 0 {
		return ""
	}
	total := 0
	for _, s := range saved {
		total += s.Bytes
	}
	noun := "images"
	if len(saved) == 1 {
		noun = "image"
	}
	return fmt.Sprintf("%d %s (%s)", len(saved), noun, size(total))
}

func size(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func describeName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return "unnamed"
}

// slug turns a conversation id into one directory name. Conversation
// ids come off the wire, so this is a fence as much as a tidy-up: no
// separators, no dots, nothing that walks out of the store.
func slug(conversation string) string {
	var b strings.Builder
	for _, r := range conversation {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "loose"
	}
	return out
}
