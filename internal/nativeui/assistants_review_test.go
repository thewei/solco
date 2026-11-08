package nativeui

import (
	"context"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/personalagent"
)

const reviewTaskJSON = `{"id":"t_c","assistantId":"a_1","goalId":"","ruleId":"","parentTaskId":"","kind":"coding","title":"Fix the parser","brief":"b","repoPath":"/r","agent":"omp","model":"","state":"review_required","needsYou":true,"reviewState":"awaiting_approval","attempt":1,"revision":3,"createdAt":1,"updatedAt":2}`
const reviewJSON = `{"id":"v_1","taskId":"t_c","assistantId":"a_1","state":"awaiting_approval","branch":"solco/t-c","head":"abc1234","baseHead":"000","targetBranch":"main","targetHead":"111","files":2,"insertions":10,"deletions":3,"paths":["a.go","b.go"],"diffHash":"d","checks":[{"command":"go test ./...","ok":true,"exitCode":0,"output":"","durationMs":5}],"agentReviewTaskId":"","approvalId":"p_1","error":"","revision":2,"createdAt":1,"updatedAt":2}`

func TestDrawerCarriesACodingTasksReviewAndItsMergeCard(t *testing.T) {
	stubAssistants(t, false) // the inline lane; the client is replaced below
	var calls [][2]string
	newAssistantsClient = func(string, string) *personalagent.Client {
		return personalagent.NewClientWithExec(func(_ context.Context, stdin string, args ...string) ([]byte, error) {
			line := strings.Join(args[1:], " ")
			calls = append(calls, [2]string{line, stdin})
			switch {
			case line == "overview":
				return []byte(assistantsOverviewJSON), nil
			case line == "task list --assistant a_1":
				return []byte(`{"schema":1,"tasks":[` + reviewTaskJSON + `]}`), nil
			case line == "task show t_c":
				return []byte(`{"schema":1,"task":` + reviewTaskJSON + `,"runs":[]}`), nil
			case line == "review show t_c":
				return []byte(`{"schema":1,"review":` + reviewJSON + `}`), nil
			case line == "approval list --assistant a_1":
				return []byte(`{"schema":1,"approvals":[{"id":"p_1","assistantId":"a_1","subjectKind":"task","subjectId":"t_c","action":"Merge","actionHash":"h","expectedRevision":3,"expiresAt":9,"state":"approved","decidedAt":3,"createdAt":1}]}`), nil
			case line == "goal list --assistant a_1":
				return []byte(`{"schema":1,"goals":[]}`), nil
			case strings.HasPrefix(line, "review "):
				return []byte(`{"schema":1,"review":` + reviewJSON + `}`), nil
			}
			return []byte(`{"schema":1}`), nil
		})
	}
	b := NewAssistantsBridge(openAssistantsShell(t))
	ctx := context.Background()

	d, err := b.Drawer(ctx, "a_1")
	if err != nil || len(d.Tasks) != 1 || d.Tasks[0].Review == nil {
		t.Fatalf("drawer = %+v err=%v", d, err)
	}
	rv := d.Tasks[0].Review
	if rv.State != "awaiting_approval" || rv.ApprovalState != "approved" || rv.Files != 2 || len(rv.Checks) != 1 {
		t.Fatalf("review = %+v", rv)
	}

	calls = nil
	if err := b.ResumeReview(ctx, "r1", "v_1", "add a test"); err != nil {
		t.Fatal(err)
	}
	if err := b.RejectReview(ctx, "r2", "v_1", "wrong approach"); err != nil {
		t.Fatal(err)
	}
	if err := b.StartReview(ctx, "r3", "t_c"); err != nil {
		t.Fatal(err)
	}
	if err := b.AcceptReview(ctx, "r4", "v_1"); err != nil {
		t.Fatal(err)
	}
	if err := b.MergeReview(ctx, "v_1"); err != nil {
		t.Fatal(err)
	}
	var verbs []string
	var reviewCalls [][2]string
	for _, c := range calls {
		if strings.HasPrefix(c[0], "review ") {
			verbs = append(verbs, c[0])
			reviewCalls = append(reviewCalls, c)
		}
	}
	want := "review resume v_1|review reject v_1|review start t_c|review request-merge v_1|review merge v_1"
	if strings.Join(verbs, "|") != want {
		t.Fatalf("verbs = %v", verbs)
	}
	if !strings.Contains(reviewCalls[0][1], `"feedback":"add a test"`) || !strings.Contains(reviewCalls[1][1], `"reason":"wrong approach"`) {
		t.Fatalf("bodies = %q %q", reviewCalls[0][1], reviewCalls[1][1])
	}
}

// A task that is not coding, or has no evidence yet, carries no review (and
// costs no extra call).
func TestDrawerSkipsReviewForTasksWithoutEvidence(t *testing.T) {
	stubAssistants(t, false)
	b := NewAssistantsBridge(openAssistantsShell(t))
	d, err := b.Drawer(context.Background(), "a_1")
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range d.Tasks {
		if task.Review != nil {
			t.Fatalf("unexpected review on %s", task.ID)
		}
	}
}
