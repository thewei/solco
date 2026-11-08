package agenthooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TrustWorktrees records, in Claude Code's own ~/.claude.json, that the folder
// Herdr keeps its worktrees in (~/.herdr/worktrees) is trusted. Claude walks up
// from the folder it starts in, so one entry covers every worktree an
// automation run creates and the "trust this folder" question never opens
// (measured 2026-11-03: a brand-new directory below it starts without one).
//
// It is one added key and nothing else: an existing entry — including an
// explicit "no" — is never touched, every other byte of the file is preserved
// (the new entry is spliced in at the head of the "projects" object), the file
// keeps its mode, and the write is atomic. The boolean says whether the file
// changed. Codex has no such inheritance (its trust is per repository root) and
// is answered at run time by the automations plugin instead.
func (m *Manager) TrustWorktrees() (bool, error) {
	path := filepath.Join(m.home, ".claude.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil // Claude Code has never run here; nothing to pre-answer
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	dir := filepath.Join(m.home, ".herdr", "worktrees")
	out, changed, err := withTrustedProject(raw, dir)
	if err != nil || !changed {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if err := atomicWrite(path, out, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}

// withTrustedProject splices a trusted record for dir into the "projects"
// object of Claude's config, unless the object already has one for it.
func withTrustedProject(raw []byte, dir string) ([]byte, bool, error) {
	var probe struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, false, fmt.Errorf("parse claude config: %w", err)
	}
	if _, ok := probe.Projects[dir]; ok || probe.Projects == nil {
		return raw, false, nil // already has a record (never override it), or no projects to extend
	}
	at, empty, err := projectsOpenOffset(raw)
	if err != nil {
		return nil, false, err
	}
	key, _ := json.Marshal(dir)
	record := `{"allowedTools":[],"mcpContextUris":[],"mcpServers":{},"enabledMcpjsonServers":[],"disabledMcpjsonServers":[],"hasTrustDialogAccepted":true,"hasClaudeMdExternalIncludesApproved":false,"hasClaudeMdExternalIncludesWarningShown":false}`
	entry := "\n    " + string(key) + ": " + record
	if !empty {
		entry += ","
	}
	var buf bytes.Buffer
	buf.Write(raw[:at])
	buf.WriteString(entry)
	buf.Write(raw[at:])
	return buf.Bytes(), true, nil
}

// projectsOpenOffset finds the byte just after the "{" that opens the
// top-level "projects" object, and whether that object has no members.
func projectsOpenOffset(raw []byte) (int, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, false, errors.New("claude config is not an object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, false, err
		}
		if keyTok == "projects" {
			if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
				return 0, false, errors.New("claude config: projects is not an object")
			}
			return int(dec.InputOffset()), !dec.More(), nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return 0, false, err
		}
	}
	return 0, false, errors.New("claude config: no projects object")
}
