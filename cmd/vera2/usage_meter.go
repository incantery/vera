// Reporting what is left, on a timer.
//
// Gauges rather than counters: a percentage of a limit is a level, not
// a total, and it goes DOWN when the window rolls over. A counter would
// make every reset look like a data loss.
package main

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type usageMeter struct {
	used     metric.Float64Gauge
	resetsIn metric.Float64Gauge
	requests metric.Int64Gauge
	sessions metric.Int64Gauge
}

func newUsageMeter() usageMeter {
	m := otel.Meter("vera2/claude-usage")
	// Labels stay bounded: two or three windows, and a handful of
	// models that will ever appear.
	used, _ := m.Float64Gauge("claude_code.limit.used_ratio",
		metric.WithDescription("How much of a Claude Code subscription window is spent, 0–1."))
	resets, _ := m.Float64Gauge("claude_code.limit.resets_in_seconds",
		metric.WithUnit("s"), metric.WithDescription("How long until the window rolls over."))
	requests, _ := m.Int64Gauge("claude_code.local.requests_24h",
		metric.WithDescription("Requests in the last 24h, as this machine sees them."))
	sessions, _ := m.Int64Gauge("claude_code.local.sessions_24h",
		metric.WithDescription("Sessions in the last 24h, as this machine sees them."))
	return usageMeter{used, resets, requests, sessions}
}

func (um usageMeter) record(ctx context.Context, u Usage) {
	report := func(window, model string, l Limit) {
		attrs := []attribute.KeyValue{attribute.String("window", window)}
		if model != "" {
			attrs = append(attrs, attribute.String("model", model))
		}
		um.used.Record(ctx, l.Used, metric.WithAttributes(attrs...))
		if !l.Resets.IsZero() {
			um.resetsIn.Record(ctx, time.Until(l.Resets).Seconds(), metric.WithAttributes(attrs...))
		}
	}
	report("session", "", u.Session)
	report("week", "", u.Week)
	for model, l := range u.ByModel {
		report("week", model, l)
	}
	um.requests.Record(ctx, int64(u.Requests))
	um.sessions.Record(ctx, int64(u.Sessions))
}

// watchUsage polls until the context ends. It reports the first reading
// immediately — a dashboard that is blank for the first fifteen minutes
// looks broken.
func watchUsage(ctx context.Context, every time.Duration) {
	um := newUsageMeter()
	scrape := func() {
		u, err := scrapeUsage(ctx)
		if err != nil {
			// Loud, and never a zero: a limit gauge that silently reads
			// 0% is indistinguishable from a fresh week.
			slog.Error("claude usage", "error", err.Error())
			return
		}
		um.record(ctx, u)
		slog.Info("claude usage",
			"session_used", u.Session.Used,
			"week_used", u.Week.Used,
			"by_model", u.ByModel,
			"requests_24h", u.Requests,
			"sessions_24h", u.Sessions,
			"on_subscription", u.OnPlan,
		)
	}

	scrape()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scrape()
		}
	}
}
