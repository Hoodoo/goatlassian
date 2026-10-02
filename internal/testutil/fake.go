// Package testutil fakes the sibling tools for tests.
package testutil

import (
	"context"
	"fmt"
	"strings"
)

// Runner answers commands from a table keyed by "name arg arg...". A key
// ending in "*" matches any command with that prefix.
type Runner map[string]string

// Run implements sources.Runner.
func (r Runner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if out, ok := r[key]; ok {
		return []byte(out), nil
	}
	best := ""
	for k := range r {
		if p, ok := strings.CutSuffix(k, "*"); ok && strings.HasPrefix(key, p) && len(p) > len(best) {
			best = k
		}
	}
	if best != "" {
		return []byte(r[best]), nil
	}
	return nil, fmt.Errorf("%s: exit status 1: unexpected command", key)
}
