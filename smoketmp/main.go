package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/incantery/vera/events"
)

func main() {
	dir := os.Args[1]
	g := &events.GitWatcher{Dir: dir, Lookback: 14 * 24 * time.Hour}
	log := &events.Log{Dir: dir}
	repos := []events.Repo{
		{Name: "vera", Root: "/Users/sethlowie/go/src/github.com/incantery/vera"},
		{Name: "rook", Root: "/Users/sethlowie/go/src/github.com/incantery/rook"},
	}
	evs := g.ScanAll(context.Background(), repos)
	fmt.Println("scanned:", len(evs))
	if err := log.Append(evs...); err != nil {
		fmt.Println("append:", err)
	}
	// A second sweep must say nothing.
	again := g.ScanAll(context.Background(), repos)
	fmt.Println("second sweep:", len(again))

	got, err := events.Read(dir, events.Query{Limit: 400})
	if err != nil {
		fmt.Println("read:", err)
		return
	}
	fmt.Print(events.Summarize(got, time.Now().AddDate(0, 0, -14), time.Now()).Text())
}
