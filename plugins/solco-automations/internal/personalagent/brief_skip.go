package personalagent

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// A restarted Coordinator continues its own OMP conversation (--resume), and
// the standing brief — instructions, the Assistant's memory and the recovered
// state — is typed into it again at every start. When that text would be
// byte-identical to what the transcript being resumed already holds, typing it
// again only makes the Agent answer a message nobody sent. So the launch hash
// is remembered together with the transcript it was delivered into, and a
// matching restart types nothing.
//
// Anything that changes the text (new instructions, edited memory, a task that
// moved, another Agent, a different CLI path) changes the hash and the brief is
// sent as before; so does a transcript that is not the recorded one, and an old
// delivery: OMP compacts long conversations, which may drop the brief.

// briefMaxAge bounds how long a delivered brief is trusted to still be in the
// Agent's context.
const briefMaxAge = 24 * time.Hour

// hashBrief fingerprints a Coordinator's whole launch prompt.
func hashBrief(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// briefAlreadyIn reports whether the transcript being resumed holds this exact
// launch prompt, delivered recently. Any doubt answers false: the brief is then
// sent, which is the safe direction.
func (s *Store) briefAlreadyIn(assistantID, transcript, hash string) bool {
	if transcript == "" || hash == "" {
		return false
	}
	var gotHash, gotSession string
	var at int64
	err := s.db.QueryRow(`SELECT brief_hash,brief_session,brief_at FROM persona_applied WHERE assistant_id=? AND surface='coordinator'`,
		assistantID).Scan(&gotHash, &gotSession, &at)
	if err != nil || gotHash != hash || gotSession != transcript {
		return false
	}
	age := s.nowMS() - at
	return age >= 0 && age < briefMaxAge.Milliseconds()
}

// recordBrief notes that the Coordinator's turn on this launch prompt finished
// in the transcript. The row exists since the launch marked its instruction
// revision; without one nothing is recorded and the next start sends the brief.
func (s *Store) recordBrief(assistantID, hash, transcript string) {
	if hash == "" || transcript == "" {
		return
	}
	_, _ = s.db.Exec(`UPDATE persona_applied SET brief_hash=?, brief_session=?, brief_at=? WHERE assistant_id=? AND surface='coordinator'`,
		hash, transcript, s.nowMS(), assistantID)
}
