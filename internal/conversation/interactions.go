package conversation

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// InteractionKind is the structured provider interaction vocabulary (0.6
// §17): permission requests and questions, never free-form prompts.
type InteractionKind string

const (
	InteractionPermission InteractionKind = "permission"
	InteractionQuestion   InteractionKind = "question"
)

// PendingInteraction is one blocked interaction awaiting resolution.
type PendingInteraction struct {
	ID             string
	ConversationID ConversationID
	AgentPaneID    string
	Fingerprint    string
	Kind           InteractionKind
	Prompt         string
	// Options are the provider's own labeled actions (e.g. Approve / Deny);
	// resolving with anything outside them fails closed.
	Options []string
	// Header is the provider's short question label and Descriptions the
	// per-option explanations (parallel to Options; "" when none). Both are
	// presentation only — resolution still keys on the exact option label.
	Header       string
	Descriptions []string
	// Start is the option row the provider's selector highlights when the
	// prompt opens; arrow navigation is relative to it (0 = first row).
	Start int
	// Step/Steps place a question of a transcript `ask` call (Steps > 0,
	// Step 0-based; Steps == 1 is the single-question ask). Zero for
	// screen-scraped prompts. Presentation and answer bookkeeping only.
	Step, Steps int
	// EscapeOption names the option the provider has no row for but takes
	// Escape as (omp's closing review sheet: Submit is a row, Cancel is ⎋).
	// Answering with it sends Escape instead of arrow navigation.
	EscapeOption string
	// Multi marks a multi-select question: the user ticks any subset of
	// Options and submits once (ResolveSetCAS), instead of picking one.
	Multi bool
	// Picker marks a card read off a Provider's list picker (/model, …)
	// that Solco itself opened: it is not an agent prompt, so a card is not
	// retired by the agent leaving `blocked` — only by the picker closing.
	Picker bool
	// Commit is the key that confirms the highlighted row ("" = enter).
	// Claude Code's model picker takes `s` for "this session only".
	Commit string
	// CommitRow, for a multi-select question whose commit row ("Next") sits
	// below the options (Pi's ask_user-question dialog: Type something. then
	// Next), is that row's index; the cursor opens on row 0 and enter on an
	// option toggles it, so the commit means walking down to this row. 0 = the
	// omp rule (enter on the last picked row submits).
	CommitRow int
	// ParkedWorking marks a prompt the agent's host keeps reporting as
	// `working` while it is open (Pi's ask_user_question dialog sends no
	// blocked signal): the card is born from the transcript's unanswered call
	// and retired when that call gets its result, never by the status.
	ParkedWorking bool
	// OptionKeys, when set (one entry per Option), are the exact named keys
	// that choose each option; they replace arrow navigation. Cursor Agent's
	// approval sheet answers to `y` / `n`, not to a highlighted row.
	OptionKeys [][]string
}

// InteractionResolution records the first-winner resolution.
type InteractionResolution struct {
	InteractionID string
	Option        string
	ResolvedAtMS  int64
}

// ErrInteractionResolved reports a lost CAS race.
var ErrInteractionResolved = errors.New("interaction already resolved")

// ErrUnknownOption reports a resolution outside the provider's options:
// blocked interactions may only be answered through their exact bridge.
var ErrUnknownOption = errors.New("resolution is not one of the provider options")

// InteractionBroker resolves blocked interactions with first-winner CAS and
// occupant-change cancellation (0.6 §17).
type InteractionBroker struct {
	mu           sync.Mutex
	pending      map[string]PendingInteraction
	resolutions  map[string]InteractionResolution
	resolveClock func() int64
}

func NewInteractionBroker(resolveClock func() int64) *InteractionBroker {
	if resolveClock == nil {
		resolveClock = unixMilli
	}
	return &InteractionBroker{
		pending:      make(map[string]PendingInteraction),
		resolutions:  make(map[string]InteractionResolution),
		resolveClock: resolveClock,
	}
}

func unixMilli() int64 { return time.Now().UnixMilli() }

// Publish registers one pending interaction; a newer interaction for the
// same conversation cancels the older pending one (occupant/lifecycle
// supersession).
func (b *InteractionBroker) Publish(interaction PendingInteraction) error {
	if interaction.ID == "" || interaction.ConversationID == "" || interaction.Prompt == "" {
		return errors.New("interaction requires id, conversation and prompt")
	}
	if len(interaction.Options) == 0 {
		return errors.New("interaction requires provider options")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, existing := range b.pending {
		if existing.ConversationID == interaction.ConversationID {
			delete(b.pending, id)
		}
	}
	b.pending[interaction.ID] = interaction
	return nil
}

// Pending lists unresolved interactions.
func (b *InteractionBroker) Pending() []PendingInteraction {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := make([]PendingInteraction, 0, len(b.pending))
	for _, interaction := range b.pending {
		pending = append(pending, interaction)
	}
	return pending
}

// ResolveCAS resolves with compare-and-swap semantics: exactly the first
// caller wins; later resolvers of the same interaction get
// ErrInteractionResolved. The option must be one of the provider's own.
func (b *InteractionBroker) ResolveCAS(interactionID, option string) (InteractionResolution, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if resolution, ok := b.resolutions[interactionID]; ok {
		return resolution, ErrInteractionResolved
	}
	interaction, ok := b.pending[interactionID]
	if !ok {
		return InteractionResolution{}, errors.New("unknown interaction")
	}
	matched := false
	for _, candidate := range interaction.Options {
		if candidate == option {
			matched = true
			break
		}
	}
	if !matched {
		return InteractionResolution{}, ErrUnknownOption
	}
	resolution := InteractionResolution{
		InteractionID: interactionID,
		Option:        option,
		ResolvedAtMS:  b.resolveClock(),
	}
	b.resolutions[interactionID] = resolution
	delete(b.pending, interactionID)
	return resolution, nil
}

// ResolveSetCAS is ResolveCAS for a multi-select interaction: every picked
// option must be one of the provider's own, an empty pick is allowed (the
// provider decides whether it accepts one), and the resolution records the
// picks joined by newlines.
func (b *InteractionBroker) ResolveSetCAS(interactionID string, picks []string) (InteractionResolution, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if resolution, ok := b.resolutions[interactionID]; ok {
		return resolution, ErrInteractionResolved
	}
	interaction, ok := b.pending[interactionID]
	if !ok {
		return InteractionResolution{}, errors.New("unknown interaction")
	}
	if !interaction.Multi {
		return InteractionResolution{}, errors.New("not a multi-select interaction")
	}
	for _, pick := range picks {
		if !slices.Contains(interaction.Options, pick) {
			return InteractionResolution{}, ErrUnknownOption
		}
	}
	resolution := InteractionResolution{
		InteractionID: interactionID,
		Option:        strings.Join(picks, "\n"),
		ResolvedAtMS:  b.resolveClock(),
	}
	b.resolutions[interactionID] = resolution
	delete(b.pending, interactionID)
	return resolution, nil
}

// CancelForPane drops every pending interaction of one agent pane: the
// pane's lifecycle moved on (the agent is no longer blocked), so its
// questions are gone regardless of which conversation id they carried.
func (b *InteractionBroker) CancelForPane(paneID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for interactionID, interaction := range b.pending {
		if interaction.AgentPaneID == paneID {
			delete(b.pending, interactionID)
		}
	}
}

// PendingForPane lists one pane's unresolved interactions.
func (b *InteractionBroker) PendingForPane(paneID string) []PendingInteraction {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := make([]PendingInteraction, 0, 2)
	for _, interaction := range b.pending {
		if interaction.AgentPaneID == paneID {
			pending = append(pending, interaction)
		}
	}
	return pending
}

// CancelForConversation drops a pending interaction when the occupant
// changed or the lifecycle moved on.
func (b *InteractionBroker) CancelForConversation(id ConversationID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for interactionID, interaction := range b.pending {
		if interaction.ConversationID == id {
			delete(b.pending, interactionID)
		}
	}
}
