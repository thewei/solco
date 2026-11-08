package personalagent

import "fmt"

// The one next step a stalled goal's reminder names (Gap A07). It is chosen
// from the plan by code, not left to a model: a blocked step is replanned
// while the replan budget lasts and handed to the person once it is spent; a
// step only a person can confirm goes to the person; everything else waits for
// work already in flight. Replanning itself stays the Coordinator's call
// through `goal set-plan` (bounded by MaxReplans); this only says which road.
const (
	NextReplan   = "replan"
	NextAskOwner = "ask_owner"
	NextWait     = "wait"
)

func nextStep(p GoalPlan) (action, why string) {
	for _, m := range p.Milestones {
		if m.State != MilestoneBlocked {
			continue
		}
		if p.Replans < MaxReplans {
			return NextReplan, fmt.Sprintf("%q is blocked — revise the plan (%d of %d revisions used)", m.Title, p.Replans, MaxReplans)
		}
		return NextAskOwner, fmt.Sprintf("%q is blocked and the plan was already revised %d times — ask the person", m.Title, MaxReplans)
	}
	for _, m := range p.Milestones {
		if m.State == MilestoneNeedsYou {
			return NextAskOwner, fmt.Sprintf("%q needs the person's confirmation", m.Title)
		}
	}
	return NextWait, "work is in flight or waiting for review"
}
