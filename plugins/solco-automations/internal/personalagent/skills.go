// [INPUT]: a Task's named skills, the folders OMP loads skills from, the Assistant's grants
// [OUTPUT]: DiscoverSkills, skill grants and the approval card that asks for one
// [POS]: Gap W04/C10 — a named skill is found, authorised by a person, and shown used; never replaced by another tool
// [PROTOCOL]: Update header on changes; read project CLAUDE.md
package personalagent

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SkillInfo is a skill OMP can load.
type SkillInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

const maxRequiredSkills = 8

// Skill-stop reasons are stable prefixes of a failed run's error.
const (
	SkillMissingPrefix    = "skill_missing: "
	SkillNotGrantedPrefix = "skill_not_granted: "
	SkillNotUsedPrefix    = "skill_not_used: "
)

// skillRoots are the user-level folders OMP reads skills from (measured in
// OMP's discovery code: ~/.omp/agent/skills, ~/.agents/skills, ~/.agent/skills).
// Claude's own folder is read only when OMP's setting enables it, so it is not
// counted here: a skill Solco promises must be one OMP loads by default.
func skillRoots(home string) []string {
	return []string{
		filepath.Join(home, ".omp", "agent", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".agent", "skills"),
	}
}

// DiscoverSkills lists installed skills by name (first root wins).
func DiscoverSkills(home string) map[string]SkillInfo {
	out := map[string]SkillInfo{}
	for _, root := range skillRoots(home) {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			if st, err := os.Stat(dir); err != nil || !st.IsDir() { // follows symlinks
				continue
			}
			md := filepath.Join(dir, "SKILL.md")
			if st, err := os.Stat(md); err != nil || !st.Mode().IsRegular() {
				continue
			}
			if _, seen := out[e.Name()]; !seen {
				out[e.Name()] = SkillInfo{Name: e.Name(), Path: md}
			}
		}
	}
	return out
}

func normalizeSkills(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, n := range in {
		n = strings.TrimSpace(n)
		if !skillNameRe.MatchString(n) {
			return nil, fmt.Errorf("%w: skill name %q", ErrInvalid, n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > maxRequiredSkills {
		return nil, fmt.Errorf("%w: at most %d required skills", ErrInvalid, maxRequiredSkills)
	}
	sort.Strings(out)
	return out, nil
}

// TaskSkills lists the skills a task must use.
func (s *Store) TaskSkills(taskID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT skill FROM task_skills WHERE task_id=? ORDER BY skill`, taskID)
	if err != nil {
		return nil, wrapBusy(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SkillGranted reports whether a person let this Assistant use the skill.
func (s *Store) SkillGranted(assistantID, skill string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM skill_grants WHERE assistant_id=? AND skill=?`, assistantID, skill).Scan(&n)
	return n > 0, wrapBusy(err)
}

// GrantSkill records a grant directly (the approval card is the normal door).
func (s *Store) GrantSkill(assistantID, skill string) error {
	if !skillNameRe.MatchString(skill) {
		return fmt.Errorf("%w: skill name %q", ErrInvalid, skill)
	}
	_, _, err := execute(s, "", "skill.grant", nil, func(tx *sql.Tx) (struct{}, error) {
		if _, err := getAssistantTx(tx, assistantID); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, s.grantSkillTx(tx, assistantID, skill)
	})
	return err
}

// grantSkillTx grants and requeues exactly the tasks this skill's missing grant
// stopped (their latest run failed with skill_not_granted for it).
func (s *Store) grantSkillTx(tx *sql.Tx, assistantID, skill string) error {
	now := s.nowMS()
	if _, err := tx.Exec(`INSERT INTO skill_grants(assistant_id,skill,granted_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, assistantID, skill, now); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE tasks SET state=?, attempt=attempt+1, needs_you=0, revision=revision+1, updated_at=?
		WHERE assistant_id=? AND state=? AND id IN (SELECT task_id FROM task_skills WHERE skill=?)
		AND (SELECT error FROM runs WHERE task_id=tasks.id ORDER BY created_at DESC, rowid DESC LIMIT 1) LIKE ?`,
		TaskQueued, now, assistantID, TaskFailed, skill, SkillNotGrantedPrefix+skill+"%")
	return err
}

func skillSubject(assistantID, skill string) string { return assistantID + ":" + skill }

func skillSubjectOf(assistantID, subject string) (string, error) {
	skill, ok := strings.CutPrefix(subject, assistantID+":")
	if !ok || !skillNameRe.MatchString(skill) {
		return "", fmt.Errorf("%w: skill subject belongs to another assistant", ErrInvalid)
	}
	return skill, nil
}

// askSkillApproval puts one pending card in front of the person, unless one is
// already waiting for this exact Assistant and skill.
func (s *Store) askSkillApproval(assistantID, skill string, attempt int) error {
	cards, err := s.ListApprovals(assistantID)
	if err != nil {
		return err
	}
	subject := skillSubject(assistantID, skill)
	for _, c := range cards {
		if c.SubjectKind == "skill" && c.SubjectID == subject && c.State == ApprovalPending {
			return nil
		}
	}
	_, _, err = s.RequestApproval(fmt.Sprintf("skill:%s:%s:%d", assistantID, skill, attempt), RequestApprovalInput{
		AssistantID: assistantID, SubjectKind: "skill", SubjectID: subject, TTL: 7 * 24 * time.Hour,
		Action: fmt.Sprintf("Let this Assistant's workers use the skill %q. A worker loads it from your skills folder and follows its instructions, including any browser, account or command access they describe.", skill),
	})
	return err
}

// setRoutineSkills replaces the skills a routine's runs must use. The routine's
// approval card names them, so a person answers for exactly this list.
func (s *Store) setRoutineSkills(name string, skills []string) error {
	_, _, err := execute(s, "", "routine.skills", nil, func(tx *sql.Tx) (struct{}, error) {
		if _, err := tx.Exec(`DELETE FROM routine_skills WHERE routine_name=?`, name); err != nil {
			return struct{}{}, err
		}
		for _, sk := range skills {
			if _, err := tx.Exec(`INSERT INTO routine_skills(routine_name,skill) VALUES(?,?)`, name, sk); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}
