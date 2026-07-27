package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// cmdTeam shows how the specialist roles have performed — a leaderboard the
// coordinator and the user can both read to see who delivers.
func cmdTeam(_ context.Context, d Deps, _ Input) (Result, error) {
	if d.Agent == nil || d.Agent.RolePerformance() == nil {
		return Result{}, errors.New("role performance is not tracked in this runtime")
	}
	list := d.Agent.RolePerformance().List()
	if len(list) == 0 {
		return Result{Output: "No roles have been delegated to yet. Performance is recorded as the agent " +
			"hands work to specialists with the delegate tool."}, nil
	}

	var b strings.Builder
	b.WriteString("**Specialist performance**\n\n")
	b.WriteString("| Role | Score | Missions | Success | Kept |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, s := range list {
		successRate := 0
		if s.Missions > 0 {
			successRate = s.Successes * 100 / s.Missions
		}
		fmt.Fprintf(&b, "| %s | %.1f | %d | %d%% | %d |\n",
			s.Role, s.Score, s.Missions, successRate, s.Kept)
	}
	b.WriteString("\nScore weighs how often a specialist succeeds, how often its work is kept, " +
		"and how few turns it takes. An untried role starts neutral.")
	return Result{Output: b.String()}, nil
}
