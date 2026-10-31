package conversation

import (
	"errors"
	"testing"
)

func TestResolveSetCASValidatesEveryPickAndWinsOnce(t *testing.T) {
	broker := NewInteractionBroker(nil)
	if err := broker.Publish(PendingInteraction{
		ID: "ia-1", ConversationID: "c", AgentPaneID: "p", Kind: InteractionQuestion,
		Prompt: "Which?", Options: []string{"a", "b", "c"}, Multi: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.ResolveSetCAS("ia-1", []string{"a", "z"}); !errors.Is(err, ErrUnknownOption) {
		t.Fatalf("a pick outside the provider's options must fail closed: %v", err)
	}
	resolution, err := broker.ResolveSetCAS("ia-1", []string{"a", "c"})
	if err != nil || resolution.Option != "a\nc" {
		t.Fatalf("resolution = %+v err=%v", resolution, err)
	}
	if _, err := broker.ResolveSetCAS("ia-1", []string{"b"}); !errors.Is(err, ErrInteractionResolved) {
		t.Fatalf("second resolution must lose: %v", err)
	}
}
