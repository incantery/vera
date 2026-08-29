// The pictures that came with the words.
//
// Vera has no eyes. mote's provider carries text, and every model
// behind it is reached through that one shape — so a screenshot cannot
// be put in front of her however it arrives. What she DOES have is
// somebody to hand it to: Claude Code reads images off the disk, as a
// delegate and in a fleet room alike.
//
// So this file is a relay, not a viewer. An image is kept once (see
// package attach), the person's message gains a sentence saying one
// came with it and that she cannot see it, and the path rides on the
// call's Handle so that every tool that hands work to an agent can
// pass the file along without the model having to copy a path around.
//
// The moment mote's Message grows a picture, the first half of this
// changes and the second half does not.
package main

import (
	"context"
	"strings"

	"github.com/incantery/mote/tool"
	"github.com/incantery/vera/attach"
)

// keyImages is Vera's second Handle value beside keyConversation: the
// files that came with this exchange, absolute, one per line. Handle
// values are strings — mote's Value says so — and a tool that wants
// them asks with attached().
const keyImages = "images"

// imagesKey carries the same list down the context, from the exchange
// that received them to the tool call that will hand them on. A
// context value rather than another parameter on invoke: the images
// belong to the exchange, not to any one call, and every tool alike
// should be able to reach them.
type imagesKey struct{}

func withImages(ctx context.Context, paths []string) context.Context {
	if len(paths) == 0 {
		return ctx
	}
	return context.WithValue(ctx, imagesKey{}, paths)
}

func imagesOn(ctx context.Context) []string {
	paths, _ := ctx.Value(imagesKey{}).([]string)
	return paths
}

// attached is what a tool asks: the pictures that came with the
// message it is answering, or nothing at all.
func attached(h tool.Handle) []string {
	joined := strings.TrimSpace(h.Value(keyImages))
	if joined == "" {
		return nil
	}
	return strings.Split(joined, "\n")
}

// keep writes what arrived and answers with where it went.
//
// A message with no images is untouched and touches no disk — the
// text-only path is exactly what it was. A Vera with nowhere to keep
// pictures says so rather than dropping them silently: a person who
// pasted a screenshot and got an answer about nothing would have no
// way to tell what happened.
func (m *Mind) keep(msg Message) ([]attach.Saved, error) {
	if len(msg.Images) == 0 {
		return nil, nil
	}
	if m == nil || m.Attachments == nil {
		return nil, attach.ErrNoStore
	}
	return m.Attachments.Save(msg.Conversation, msg.Images)
}
