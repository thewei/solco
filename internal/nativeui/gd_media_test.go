package nativeui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 真实临时 Git 仓库里已提交/修改/新增的 PNG 与非图片二进制，headless Diff 审阅
 * [OUTPUT]: 图片前/后并排（含尺寸与大小）、新增只显示一侧、非图片显示大小变化、过大图片的说明
 * [POS]: gd_media.go 的回归；headless 无 GPU，只断言文字与读取结果，不断言位图
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mediaShell(t *testing.T, root string) (*Shell, *ui.Tester) {
	t.Helper()
	s := gitSurfaceTestShell(t, root)
	s.git.rootOf[root] = root
	snap, err := s.git.runner.Snapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	s.git.snapshot = snap
	s.git.refreshing = true
	s.router.Push(routeWorkspace)
	s.showSurface(WorkspaceSurfaceDiff)
	s.rebuildDiffRows()
	tester := ui.NewTester(s.View, 1280, 900)
	settleFrames(tester, 4)
	return s, tester
}

func TestBinaryChangesPreviewInTheCard(t *testing.T) {
	root := t.TempDir()
	gitScript(t, root,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "user.name", "Test"},
	)
	writeRepoFile(t, root, "logo.png", string(pngBytes(t, 2, 2, color.RGBA{255, 0, 0, 255})))
	writeRepoFile(t, root, "data.bin", "\x00\x01\x02\x03")
	gitScript(t, root, []string{"add", "."}, []string{"commit", "-q", "-m", "base"})
	writeRepoFile(t, root, "logo.png", string(pngBytes(t, 3, 5, color.RGBA{0, 0, 255, 255})))
	writeRepoFile(t, root, "data.bin", "\x00\x01\x02\x03\x04\x05\x06")
	writeRepoFile(t, root, "new.png", string(pngBytes(t, 4, 4, color.RGBA{0, 255, 0, 255})))
	writeRepoFile(t, root, "huge.png", "\x00not a png "+strings.Repeat("x", 100))

	s, tester := mediaShell(t, root)
	var logo *gdFile
	for _, f := range s.git.gdFiles {
		if f.cf.Path == "logo.png" {
			logo = f
		}
	}
	if logo == nil || !logo.cf.Binary {
		t.Fatalf("logo.png is not a binary change; texts=%q", tester.Texts())
	}
	m := logo.media
	if m.state != gdMediaReady || !m.image {
		t.Fatalf("logo.png preview state = %v image=%v", m.state, m.image)
	}
	if m.before.w != 2 || m.before.h != 2 || m.after.w != 3 || m.after.h != 5 {
		t.Fatalf("sizes before %dx%d after %dx%d, want 2x2 and 3x5", m.before.w, m.before.h, m.after.w, m.after.h)
	}
	for _, want := range []string{"Before", "After", "2 × 2", "3 × 5", "Added", "4 × 4"} {
		found := false
		for _, text := range tester.Texts() {
			if strings.Contains(text, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %q in the cards; texts=%q", want, tester.Texts())
		}
	}
	found := false
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "Binary file · 4 B → 7 B") || strings.HasPrefix(text, "Binary file · 4 bytes → 7 bytes") ||
			(strings.HasPrefix(text, "Binary file · ") && strings.Contains(text, "→")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("data.bin shows no size change; texts=%q", tester.Texts())
	}
	for _, f := range s.git.gdFiles {
		if f.cf.Path == "huge.png" && f.media.after.note != "Can't preview this image" {
			t.Fatalf("an undecodable .png says %q, want the plain refusal", f.media.after.note)
		}
	}
}

// A commit review shows the commit's own two sides, not the work tree's.
func TestBinaryCommitReviewReadsTheCommitsBlobs(t *testing.T) {
	root := t.TempDir()
	gitScript(t, root,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "test@example.com"},
		[]string{"config", "user.name", "Test"},
	)
	writeRepoFile(t, root, "logo.png", string(pngBytes(t, 2, 2, color.RGBA{255, 0, 0, 255})))
	gitScript(t, root, []string{"add", "."}, []string{"commit", "-q", "-m", "one"})
	writeRepoFile(t, root, "logo.png", string(pngBytes(t, 6, 3, color.RGBA{0, 0, 255, 255})))
	gitScript(t, root, []string{"add", "."}, []string{"commit", "-q", "-m", "two"})
	// The work tree moves on after the commit under review.
	writeRepoFile(t, root, "logo.png", string(pngBytes(t, 9, 9, color.RGBA{0, 255, 0, 255})))

	s, _ := mediaShell(t, root)
	hash := strings.TrimSpace(gitOutput(t, root, "rev-parse", "HEAD"))
	commitSnap, err := s.git.runner.CommitSnapshot(context.Background(), root, hash)
	if err != nil {
		t.Fatal(err)
	}
	s.git.source = gdSourceCommit
	s.git.commitHash = hash
	s.git.commitSnap = commitSnap
	s.git.gdFilesFor = nil
	s.rebuildDiffRows()
	var f *gdFile
	for _, g := range s.git.gdFiles {
		if g.cf.Path == "logo.png" {
			f = g
		}
	}
	if f == nil {
		t.Fatal("logo.png missing from the commit review")
	}
	s.gdLoadMedia(f)
	if f.media.before.w != 2 || f.media.after.w != 6 || f.media.after.h != 3 {
		t.Fatalf("commit sides before %dx%d after %dx%d, want 2x2 and 6x3 (not the work tree's 9x9)",
			f.media.before.w, f.media.before.h, f.media.after.w, f.media.after.h)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
