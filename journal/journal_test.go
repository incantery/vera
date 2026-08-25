package journal

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w := &Writer{Dir: dir}
	e := Entry{At: time.Now(), Conversation: "chat-1", Said: "hi", Answered: "hello",
		Rounds: []Round{{Tool: "fleet", Args: json.RawMessage(`{"action":"list"}`), Result: "ok", Task: "abc"},
			{Tool: "delegate", Args: json.RawMessage(`not json`), Result: "x"}}}
	if err := w.Write(e); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(Entry{Conversation: "chat-1", Said: "again", Answered: "yes"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(Path(dir, "chat-1"))
	if err != nil || len(got) != 2 {
		t.Fatalf("read: %v %d", err, len(got))
	}
	if got[0].Rounds[0].Task != "abc" || string(got[0].Rounds[1].Args) != `"not json"` {
		t.Errorf("rounds: %+v", got[0].Rounds)
	}
	files, _ := List(dir)
	if len(files) != 1 || files[0].Conversation != "chat-1" || files[0].Path != filepath.Join(dir, "chat-1.jsonl") {
		t.Errorf("list: %+v", files)
	}
	if Path(dir, "") != filepath.Join(dir, "stateless.jsonl") || Path(dir, "a/b") != filepath.Join(dir, "a_b.jsonl") {
		t.Error("names")
	}
}
