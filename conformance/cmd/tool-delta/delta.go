package main

import (
	"fmt"
	"io"
	"time"
)

// idKey is the pair the baseline is searched by. A result is new when its
// pair is absent, not when the baseline holds fewer copies.
type idKey struct {
	rule string
	id   string
}

// printed is the three kinds of line, kept apart so the summary can count them.
type printed struct {
	neu     []string
	waived  []string
	expired []string
}

func diff(current, base []finding, waivers []waiver, today time.Time) printed {
	known := make(map[idKey]struct{}, len(base))
	for _, f := range base {
		known[idKey{f.ruleID, f.identity}] = struct{}{}
	}
	var p printed
	for _, f := range current {
		if _, ok := known[idKey{f.ruleID, f.identity}]; ok {
			continue
		}
		waived, until, expired := matchWaivers(f, waivers, today)
		if waived {
			p.waived = append(p.waived, fmt.Sprintf("WAIVED %s %s until %s", f.ruleID, f.identity, until))
		} else {
			p.neu = append(p.neu, fmt.Sprintf("NEW %s %s %s:%d", f.ruleID, f.identity, f.uri, f.line))
		}
		for _, w := range expired {
			p.expired = append(p.expired, fmt.Sprintf("EXPIRED %s %s %s expired %s", w.rule, w.fingerprint, w.reason, w.expiresText))
		}
	}
	return p
}

func writeDelta(out io.Writer, p printed) {
	for _, line := range p.neu {
		fmt.Fprintln(out, line)
	}
	for _, line := range p.waived {
		fmt.Fprintln(out, line)
	}
	for _, line := range p.expired {
		fmt.Fprintln(out, line)
	}
	fmt.Fprintf(out, "delta: %d new, %d waived, %d expired\n", len(p.neu), len(p.waived), len(p.expired))
	fmt.Fprintln(out, remarkLine)
}
