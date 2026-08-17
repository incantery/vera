// Package route holds the tier table: which piece of work is worth
// which model.
//
// A human picks a model once. They set it at the start of a session and
// never pick again, because re-picking mid-task is friction nobody
// pays. That is the whole opening: vera picks per node, every node, and
// the gap between the cheapest and the strongest tier is roughly ten
// times the price per token.
//
// This lives in its own package for one reason: the ladder measures
// this table, and a copy of it inside the measurement would be free to
// drift from the copy the product runs. A green ladder grading a table
// nobody uses is worse than no ladder at all.
//
// The tier is a claim about what a piece of work is WORTH, and it is
// decided by blast radius rather than by output size:
//
//   - cheap: bounded, structured, and run constantly. A digest is four
//     lines; being wrong costs one re-read.
//   - mid: judgment whose damage stops at one node. A review that
//     misses something costs a review.
//   - strong: decides what everything downstream does. A plan drawn
//     badly is paid for by every node in the graph.
//
// The trap this table is written against: a bounded OUTPUT is not a
// bounded consequence. The judge returns one word — DONE, CONTINUE,
// ESCALATE — and it is the single most expensive place in vera to be
// wrong. A false DONE ships broken work. A false ESCALATE burns a human
// interrupt, which is the one resource vera cannot buy more of. Routing
// the judge by the size of its answer would be the most expensive
// saving in the system, so it sits at mid and not at cheap.
//
// Every claim in here is a hypothesis until the ladder has run it. The
// table is reasoning, not evidence, and the comments say why rather
// than merely what so the next person can tell which parts the numbers
// have actually earned.
//
// # What the ladder has shown so far
//
// 312 cells, both arms, $31 (2026-08-17). Nothing in the table changed
// as a result, and the reason is worth more than the numbers:
//
//   - verify → cheap: SUPPORTED. 20/20 at haiku, $0.02 a pass against
//     $0.12 at strong. The task shape — run the command, report what it
//     printed — is honest for the kind, so this one is believed.
//   - review → mid: SUPPORTED, thinly. One task discriminated across
//     the whole experiment (a slice-aliasing bug: haiku 3/10, sonnet
//     and opus 10/10). That is the right shape of evidence and not
//     enough of it.
//   - investigate → mid: UNTESTED. Both corpus tasks passed at every
//     tier; the corpus has no ceiling for this kind.
//   - implement → strong: UNTESTED, and this is the interesting one.
//     Haiku went 20/20 on the EASY corpus and 20/20 on the hard one —
//     the corpus written precisely because the easy tasks could not
//     ask enough — at a third of opus's price, mostly in a single turn.
//
// The implement result is bounded much more tightly than it looks. Both
// corpora ask for the same shape of work: one file, a contract stated
// in a comment, and tests handed over that say when you are done. That
// is the most model-friendly shape coding work has, and the drive arm
// judged DONE after one turn almost everywhere. Vera's real implement
// nodes are the opposite — several files, an existing repository to
// navigate, requirements that are a sentence rather than a spec, and no
// tests until someone writes them. Nothing here licenses moving
// implement down; it only says that spec-and-test-guarded single-file
// work does not need the strongest tier.
//
// The honest conclusion is about the instrument. A synthetic corpus
// cheap enough to write is too well-specified to separate the tiers on
// implementation, and buying more repetitions of a task everything
// already passes buys nothing. The measurement that would settle it is
// vera's own board: real nodes carry a kind, a model, a cost, and an
// acceptance outcome, which is the same experiment run on work that
// actually has the shape in question. That is the instrument to build
// before spending more here.
package route

import "strings"

// Tier is what a piece of work is worth, not who runs it.
type Tier string

const (
	Cheap  Tier = "cheap"
	Mid    Tier = "mid"
	Strong Tier = "strong"
)

// Tiers, cheapest first — the order a comparison should walk.
var Tiers = []Tier{Cheap, Mid, Strong}

// The node kinds. A node's kind decides two things: whether vera may
// open it on her own, and which worker she reaches for.
const (
	KindImplement   = "implement"   // writes; the owner nods before it opens
	KindInvestigate = "investigate" // reads a codebase and reports
	KindReview      = "review"      // reads a peer's output and finds problems
	KindVerify      = "verify"      // runs the checks and reports pass/fail
	KindReconcile   = "reconcile"   // reads a disagreement and rules on it
)

// Kinds is the whole vocabulary, in the order a table should print it.
var Kinds = []string{KindImplement, KindInvestigate, KindReview, KindVerify, KindReconcile}

// ReadOnly names the kinds that cannot mutate anything by
// construction. They are the tier vera's autonomy already takes: no
// edits, no commands that write, so the worst outcome of opening one
// unasked is money, and money already has a ceiling.
func ReadOnly(kind string) bool {
	switch NormalizeKind(kind) {
	case KindInvestigate, KindReview, KindVerify, KindReconcile:
		return true
	}
	return false
}

// NormalizeKind takes what a plan or an older card carries. A card
// written before kinds existed is an implementation — that is what
// every card was — and it must not be quietly downgraded to a cheaper
// tier because its field was empty.
func NormalizeKind(s string) string {
	switch k := strings.ToLower(strings.TrimSpace(s)); k {
	case KindImplement, KindInvestigate, KindReview, KindVerify, KindReconcile:
		return k
	default:
		return KindImplement
	}
}

// Part names one of the roles vera herself plays on the wire. The
// membrane, the judge, the planner and the steward are one model
// playing several parts — this is what lets them stop being one model.
type Part string

const (
	PartDigest  Part = "digest"  // compress a finished reply
	PartExpand  Part = "expand"  // phrase the human's rough words
	PartCompile Part = "compile" // intent → the goal a drive judges by
	PartSuggest Part = "suggest" // bid on what to say next
	PartJudge   Part = "judge"   // DONE | CONTINUE | ESCALATE
	PartSteward Part = "steward" // read the whole board, propose moves
	PartPlan    Part = "plan"    // the ask → the work graph
)

// OfPart places vera's own parts. The three cheap ones are the
// high-frequency floor: a digest and an expand run on EVERY turn of
// every session, so they are where the constant cost of running vera
// actually lives, and they are bounded and structured enough to take
// the cheapest tier without argument.
func OfPart(p Part) Tier {
	switch p {
	case PartDigest, PartExpand, PartCompile:
		// Per-turn, shape-constrained, and salvaged rather than retried
		// when the shape breaks. Being wrong costs a re-read.
		return Cheap
	case PartJudge, PartSteward, PartSuggest:
		// Bounded answers, unbounded consequences. The judge gates a
		// whole rerun and every escalation to the owner; the steward
		// proposes moves on real work. See the note above on why a short
		// answer is not a cheap one.
		return Mid
	case PartPlan:
		// Drawn once per goal and paid for by every node under it. The
		// only part where spending more up front is reliably cheaper.
		return Strong
	}
	return Mid
}

// OfKind places the workers. A node's kind already says what it is for;
// that is exactly enough to say what it is worth.
func OfKind(kind string) Tier {
	switch NormalizeKind(kind) {
	case KindVerify:
		// Runs the build and the tests and reports what happened. The
		// commands do the work; the model reads output.
		return Cheap
	case KindReview, KindInvestigate:
		// Real judgment, contained: a missed finding costs one review,
		// and the reviews exist precisely because a second opinion is
		// cheaper than a wrong first one.
		return Mid
	case KindImplement, KindReconcile:
		// Implementation is the work everything else checks, and a
		// reconcile rules on a disagreement two other nodes could not
		// settle. Neither is a place to save money.
		return Strong
	}
	return Strong
}

// WorkerAlias is claude's vocabulary for each tier — ALIASES, not
// pinned model ids, and that is deliberate. An alias resolves against
// whatever the account can actually serve; a pinned id fails outright
// on a subscription without access to that model. Vera does not know
// which account her claude is logged into, so she names the tier and
// lets the CLI resolve it.
var WorkerAlias = map[Tier]string{
	Cheap:  "haiku",
	Mid:    "sonnet",
	Strong: "opus",
}

// Worker names the claude model a node of this kind earns.
func Worker(kind string) string { return WorkerAlias[OfKind(kind)] }
