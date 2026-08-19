// The drive loop is its own module so that anyone who wants the LOOP
// does not inherit the engine. vera's binary carries connectrpc, otel,
// prometheus and a proto stack; none of that is needed to say a goal,
// judge a reply, and say the next thing — this package imports the
// standard library and nothing else, and this file is the promise that
// it stays that way.
//
// rook's agent plugin is the second caller, and its root module is
// deliberately stdlib-only: requiring a module with no dependencies is
// how it can share this loop without its go.sum growing an engine.
//
// The go directive is 1.25.0 rather than the root's 1.25.7 for the
// same reason: it is the floor a consumer must meet, so it tracks the
// oldest consumer (rook), not vera's own toolchain.
module github.com/incantery/vera/drive

go 1.25.0
