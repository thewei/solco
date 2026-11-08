package personalagent

import (
	"strings"
	"testing"
)

func planWith(replans int, states ...string) GoalPlan {
	p := GoalPlan{Replans: replans, Total: len(states)}
	for i, st := range states {
		p.Milestones = append(p.Milestones, MilestoneView{Milestone: Milestone{ID: string(rune('a' + i)), Title: "step " + string(rune('A'+i))}, State: st})
	}
	return p
}

// A07: a stalled goal's reminder names ONE next step, chosen from the plan —
// never an open-ended "do something". The choice is code, bounded by the
// replan limit.
func TestNextStepChoosesFromThePlanAndStopsReplanningAtTheLimit(t *testing.T) {
	cases := []struct {
		name string
		plan GoalPlan
		act  string
	}{
		{"blocked with replans left", planWith(1, MilestoneVerified, MilestoneBlocked), NextReplan},
		{"blocked and the limit is spent", planWith(MaxReplans, MilestoneVerified, MilestoneBlocked), NextAskOwner},
		{"a person must confirm", planWith(0, MilestoneVerified, MilestoneNeedsYou), NextAskOwner},
		{"review pending", planWith(0, MilestoneVerified, MilestoneVerifying), NextWait},
		{"running", planWith(0, MilestoneWorking, MilestonePending), NextWait},
	}
	for _, c := range cases {
		act, why := nextStep(c.plan)
		if act != c.act || why == "" {
			t.Errorf("%s: %q %q, want %q", c.name, act, why, c.act)
		}
	}
	if _, why := nextStep(planWith(1, MilestoneBlocked)); !strings.Contains(why, "step A") || !strings.Contains(why, "1 of 5 revisions used") {
		t.Errorf("a replan names the blocked step and the revisions used: %q", why)
	}
}
