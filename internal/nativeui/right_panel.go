package nativeui

/**
 * [INPUT]: selected Herdr Tab CWD and bounded background directory snapshots
 * [OUTPUT]: scoped Files explorer cache and persistent URL preview controllers
 * [POS]: Files explorer storage, read by the right panel's Files page (side_panel.go owns the panel itself; the /files page retired 2026-11-08); the retired 0.10 panel's types and session hints are gone
 * [PROTOCOL]: update this header and check CLAUDE.md
 */

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/thewei/solco-gitkit/filesview"
	"github.com/thewei/solco/internal/preview"
	"sync"
	"sync/atomic"
)

type rightPanelState struct {
	// Active Files explorer data, fenced by the exact selected Tab CWD.
	expandedDirs     map[string]bool
	cachedDirEntries map[string][]filesview.Entry
	loadingDirs      map[string]bool
	currentRoot      string
	selectedFilePath string
	filesGen         atomic.Uint64

	// Ignore state (FILES-01): the merged fixed-name + git set applies per
	// root; showIgnored reveals everything the rules hide.
	showIgnored bool
	ignoredRoot string
	ignoredSet  *filesview.IgnoreSet

	// Preview tiers (FILES-09, native first since 2026-11-08): text, code and
	// Markdown source show in the framework's code editor, Markdown under
	// markdownRenderCap also as formatted text, common images as a native
	// bitmap; only AVIF and ICO ride a few hundred bytes of WebView page
	// (webImage). nativePreviewPath is the selection the state below belongs
	// to; previewSize is its stat'd size for the viewer bar and previewPlanned
	// the path whose tier plan has landed; markdownSource is the sticky
	// Rendered/Source choice. nativeMarkdown and nativeCode come from one
	// read, so the choice flips without touching the disk.
	previewSize        int64
	previewPlanned     string
	markdownSource     bool
	nativePreviewPath  string
	nativeText         *filesview.Preview
	nativeCode         *editor.State
	nativeMarkdown     *markdownPreview
	nativeTextLoading  bool
	nativeTextFailed   string
	nativeImage        *ui.Bitmap
	webImage           bool
	nativeImageLoading bool
	nativeImageFailed  string

	// reveal is the line a search hit asked the code view to show: applied
	// when that file's text lands, or at once when it is already open.
	reveal *filesReveal
}

func defaultRightPanelState() rightPanelState {
	return rightPanelState{
		expandedDirs:     make(map[string]bool),
		cachedDirEntries: make(map[string][]filesview.Entry),
		loadingDirs:      make(map[string]bool),
		ignoredSet:       filesview.NewIgnoreSet(nil),
	}
}

// syncRightPanelRoot updates the Files explorer's snapshot fence only.
// Despite the compatibility name this never opens a right panel.
func (s *Shell) syncRightPanelRoot() {
	root := s.selectedTabCWD()
	if root == s.rightPanel.currentRoot {
		return
	}
	s.rightPanel.currentRoot = root
	s.rightPanel.filesGen.Add(1)
	s.rightPanel.selectedFilePath = ""
	s.rightPanel.expandedDirs = make(map[string]bool)
	s.rightPanel.cachedDirEntries = make(map[string][]filesview.Entry)
	s.rightPanel.loadingDirs = make(map[string]bool)
	s.rightPanel.ignoredRoot = ""
	s.rightPanel.ignoredSet = filesview.NewIgnoreSet(nil)
	s.filesSearchReset()
	s.closeStaticPreview()
	s.clearNativeFilesPreview()
}

// previewManager owns persistent preview window controllers keyed by target
// (GWB-007): one controller per URL key, reused and focused, never leaked
// per click. The loopback-only boundary stays in the preview package.
type previewManager struct {
	mu          sync.Mutex
	controllers map[string]*preview.WindowController
	// report surfaces external-open failures in the shell's bounded
	// status toast; set by the Shell after construction.
	report func(string)
}

func newPreviewManager() *previewManager {
	return &previewManager{controllers: map[string]*preview.WindowController{}}
}

// openLocalPreviewURL reuses or creates the persistent controller for the
// target and focuses its window.
func (s *Shell) openLocalPreviewURL(targetURL string) {
	controller := s.previews.controllerFor(targetURL)
	if _, err := controller.OpenPreviewWindow(targetURL); err != nil {
		s.status = "Preview window unavailable"
		return
	}
	s.status = "Opened preview: " + targetURL
}

func (m *previewManager) controllerFor(targetURL string) *preview.WindowController {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.controllers[targetURL]; c != nil {
		return c
	}
	c := preview.NewWindowController(preview.WithOpenExternal(func(url string) error {
		if err := mygo.Shell.OpenExternal(url); err != nil {
			// The toast reaches the main window on its next frame; the
			// operation log line (preview package) carries the shape.
			if m.report != nil {
				m.report("Could not open " + url)
			}
			return err
		}
		return nil
	}))
	m.controllers[targetURL] = c
	return c
}
