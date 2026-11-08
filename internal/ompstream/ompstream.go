package ompstream

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

/**
 * [INPUT]: Embedded Solco-owned OMP/Pi extension bytes (provided by the application), user HOME.
 * [OUTPUT]: Read-only Diagnose and explicitly invoked Install/Uninstall of one standalone OMP or Pi extension.
 * [POS]: SC-05/SC-06 extension Doctor; no Herdr integration file, provider config, session or process mutation.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	fileName    = "solco-chat-stream.ts"
	ownedMarker = "// Managed by Solco, NOT by Herdr."
	ompAnchor   = `const streamAgent = "omp";`
	piAnchor    = `const streamAgent = "pi";`
)

type State string

const (
	Missing  State = "missing"
	Current  State = "current"
	Outdated State = "outdated"
	Foreign  State = "foreign"
	Error    State = "error"
)

type Diagnosis struct {
	State  State
	Detail string
}

type Manager struct {
	home     string
	agent    string
	agentDir string // optional Pi agent directory from PI_CODING_AGENT_DIR
	source   []byte
}

// NewManager requires the exact extension source embedded in the app binary.
// The supplied path is an injectable HOME, not an arbitrary install location.
func NewManager(home string, source []byte) *Manager {
	return &Manager{home: home, agent: "omp", source: bytes.Clone(source)}
}

// NewPiManager builds a Pi-native companion from the same App-embedded OMP
// source. Exactly one known agent identifier is substituted: the extension
// still runs inside the *existing* Herdr-owned Pi TUI, not in another process.
// If the canonical source drifts, fail closed instead of emitting an OMP tag.
func NewPiManager(home string, canonical []byte) *Manager {
	return NewPiManagerWithDir(home, "", canonical)
}

// NewPiManagerWithDir honors Pi's documented PI_CODING_AGENT_DIR override.
// An invalid relative override never silently installs into another folder.
func NewPiManagerWithDir(home, agentDir string, canonical []byte) *Manager {
	if !bytes.Contains(canonical, []byte(ompAnchor)) {
		return &Manager{home: home, agent: "pi", agentDir: agentDir}
	}
	if bytes.Count(canonical, []byte(ompAnchor)) != 1 {
		return &Manager{home: home, agent: "pi", agentDir: agentDir}
	}
	pi := bytes.Replace(canonical, []byte(ompAnchor), []byte(piAnchor), 1)
	return &Manager{home: home, agent: "pi", agentDir: agentDir, source: pi}
}

func (m *Manager) dir() string {
	if m.agent == "pi" && m.agentDir != "" {
		return filepath.Join(m.agentDir, "extensions")
	}
	return filepath.Join(m.home, "."+m.agent, "agent", "extensions")
}
func (m *Manager) path() string { return filepath.Join(m.dir(), fileName) }

// validSource refuses an empty, corrupted or unrelated embedded file.
func (m *Manager) validSource() bool {
	return (m.agent == "omp" || m.agent == "pi") && len(m.source) > 0 &&
		bytes.Contains(m.source, []byte(ownedMarker)) &&
		(m.agent != "pi" || bytes.Count(m.source, []byte(piAnchor)) == 1)
}

func (m *Manager) check() (Diagnosis, error) {
	if m.home == "" || !filepath.IsAbs(m.home) || (m.agentDir != "" && !filepath.IsAbs(m.agentDir)) || !m.validSource() {
		return Diagnosis{State: Error, Detail: "Live text extension is unavailable in this build"}, errors.New("invalid OMP stream manager")
	}
	// Do not follow an extensions-directory symlink to an arbitrary location.
	if info, err := os.Lstat(m.dir()); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return Diagnosis{State: Foreign, Detail: "Extensions folder is not a regular directory"}, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Diagnosis{State: Error, Detail: "Couldn't inspect OMP extensions"}, err
	}
	info, err := os.Lstat(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return Diagnosis{State: Missing, Detail: "Enable live text for newly started " + m.agent + " sessions"}, nil
	}
	if err != nil {
		return Diagnosis{State: Error, Detail: "Couldn't inspect OMP live text"}, err
	}
	if !info.Mode().IsRegular() {
		return Diagnosis{State: Foreign, Detail: "Extension path belongs to another file"}, nil
	}
	installed, err := os.ReadFile(m.path())
	if err != nil {
		return Diagnosis{State: Error, Detail: "Couldn't read OMP live text extension"}, err
	}
	if !bytes.Contains(installed, []byte(ownedMarker)) {
		return Diagnosis{State: Foreign, Detail: "Extension file is not managed by Solco"}, nil
	}
	if bytes.Equal(installed, m.source) {
		return Diagnosis{State: Current, Detail: "Installed; existing " + m.agent + " sessions need a restart"}, nil
	}
	return Diagnosis{State: Outdated, Detail: "Update live text for new " + m.agent + " sessions"}, nil
}

func (m *Manager) Diagnose() Diagnosis {
	result, _ := m.check()
	return result
}

// Install is called only from explicit user interaction. It never touches
// Herdr-managed extension files, edits OMP settings, or restarts any process.
func (m *Manager) Install() (Diagnosis, error) {
	state, err := m.check()
	if err != nil {
		return state, err
	}
	if state.State == Current {
		return state, nil
	}
	if state.State == Foreign || state.State == Error {
		return state, fmt.Errorf("OMP extension path is not safe to manage")
	}
	if err := os.MkdirAll(m.dir(), 0o755); err != nil {
		return m.Diagnose(), fmt.Errorf("create OMP extension directory: %w", err)
	}
	// Recheck after creating the folder: fail closed on a concurrent edit.
	fresh, err := m.check()
	if err != nil || (fresh.State != Missing && fresh.State != Outdated && fresh.State != Current) {
		return fresh, fmt.Errorf("OMP extension changed during install")
	}
	if fresh.State == Current {
		return fresh, nil
	}
	file, err := os.CreateTemp(m.dir(), ".solco-chat-stream.*")
	if err != nil {
		return m.Diagnose(), fmt.Errorf("create temporary OMP extension: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(m.source); err == nil {
		err = file.Chmod(0o644)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return m.Diagnose(), fmt.Errorf("prepare OMP extension: %w", err)
	}
	// Recheck immediately before rename; refuse to overwrite foreign files.
	current, err := m.check()
	if err != nil || (current.State != Missing && current.State != Outdated && current.State != Current) {
		return current, fmt.Errorf("OMP extension changed during install")
	}
	if current.State == Current {
		return current, nil
	}
	if err := os.Rename(temp, m.path()); err != nil {
		return m.Diagnose(), fmt.Errorf("install OMP extension: %w", err)
	}
	return m.check()
}

// Uninstall only removes the owned extension; never removes Herdr's own
// extension or an unrecognized item in the extension directory.
func (m *Manager) Uninstall() (Diagnosis, error) {
	state, err := m.check()
	if err != nil || state.State == Foreign || state.State == Error {
		return state, fmt.Errorf("OMP extension path is not safe to remove")
	}
	if state.State == Missing {
		return state, nil
	}
	if err := os.Remove(m.path()); err != nil {
		return m.Diagnose(), fmt.Errorf("remove OMP extension: %w", err)
	}
	return m.check()
}
