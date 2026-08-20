package drive

import "testing"

func TestSplitChatReply(t *testing.T) {
	cases := []struct {
		name, in, prose string
		moves           int
	}{
		{"plain prose", "Two cards are waiting on you.", "Two cards are waiting on you.", 0},
		{"answer directive", "Sending that to the worker now.\nANSWER T-136 — Retry with the smaller model.", "Sending that to the worker now.", 1},
		{"em-dash and hyphen both parse", "ok\nANSWER T-1 - yes, ship it", "ok", 1},
		{"prose mentioning answer is not a move", "The answer to T-136 is in its log already.", "The answer to T-136 is in its log already.", 0},
		{"two directives", "Done.\nANSWER T-1 — yes\nANSWER T-2 — no", "Done.", 2},
	}
	for _, c := range cases {
		prose, moves := SplitChatReply(c.in)
		if prose != c.prose || len(moves) != c.moves {
			t.Errorf("%s: prose=%q moves=%d, want %q/%d", c.name, prose, len(moves), c.prose, c.moves)
		}
	}
	_, moves := SplitChatReply("go\nANSWER T-9 — use postgres")
	if moves[0].Verb != "answer" || moves[0].Task != "T-9" || moves[0].Why != "use postgres" {
		t.Fatalf("move fields: %+v", moves[0])
	}
}
