package nativeui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/thewei/solco/internal/filesview"
	"github.com/thewei/solco/internal/gitworkbench"
)

/**
 * [INPUT]: 二进制变更的 ChangeFile 与快照（Head/DiffBase）、gdFile.view 的侧选择、gitworkbench.Runner.BlobSize/ReadBlob、filesview.DecodeImageBytes、ui.NewBitmap
 * [OUTPUT]: diff 卡片里二进制变更的预览——图片（PNG/JPEG/GIF/WebP/BMP）前/后并排（新增/删除只显示一侧）、其他二进制显示大小变化；读取与解码离 UI lane，Bitmap 回 UI lane；刷新时沿用旧画面再静默重读
 * [POS]: Diff 审阅的非文本预览（2026-10-30 用户要求）；只读，不新增 Git runner、Herdr 方法或 WebView
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	// gdMediaMaxBytes caps one side's picture; past it the card says so.
	gdMediaMaxBytes = 8 << 20
	// gdMediaMaxH is the tallest a picture is drawn.
	gdMediaMaxH = 280
	// gdMediaTimeout bounds one side's read.
	gdMediaTimeout = 10 * time.Second
)

// gdMediaState is where a binary file's preview stands.
type gdMediaState uint8

const (
	gdMediaIdle    gdMediaState = iota // not asked for yet (or asked again after a refresh)
	gdMediaLoading                     // the read runs
	gdMediaReady                       // both sides answered
)

// gdMediaSide is what one side of a binary change holds.
type gdMediaSide struct {
	present bool
	size    int64
	w, h    int
	bmp     *ui.Bitmap // nil when it is not a picture, or none was made (headless)
	note    string     // why there is no picture, "" otherwise
}

// gdMedia is the preview of a binary file's two sides.
type gdMedia struct {
	state         gdMediaState
	image         bool // the extension is one the native decoder knows
	before, after gdMediaSide
}

// gdBlobRef names one side's bytes: a Git blob ("<rev>:<path>", ":0:<path>"
// for the index) or a file of the work tree. Both empty is no such side.
type gdBlobRef struct{ git, file string }

// gdMediaRefs names the two sides of the file as the card shows it: the
// commit's own pair in a commit review, otherwise by the side the file was
// opened from (combined and staged compare HEAD, unstaged the index; the
// new side is the index for staged, the work tree for the others).
func (s *Shell) gdMediaRefs(f *gdFile, snap *gitworkbench.ChangesSnapshot) (before, after gdBlobRef) {
	cf := f.cf
	old := cf.Path
	if cf.OldPath != "" {
		old = cf.OldPath
	}
	added := cf.Status == gitworkbench.StatusAdded || cf.Status == gitworkbench.StatusUntracked
	deleted := cf.Status == gitworkbench.StatusDeleted
	if s.gdReviewingCommit() {
		from, to, _ := strings.Cut(snap.DiffBase, "..")
		if !added && from != "" {
			before = gdBlobRef{git: from + ":" + old}
		}
		if !deleted && to != "" {
			after = gdBlobRef{git: to + ":" + cf.Path}
		}
		return before, after
	}
	worktree := gdBlobRef{file: filepath.Join(s.git.root, filepath.FromSlash(cf.Path))}
	head := snap.Head
	switch f.view {
	case gdViewUnstaged:
		if !cf.Untracked {
			before = gdBlobRef{git: ":0:" + old}
		}
		if !deleted {
			after = worktree
		}
	case gdViewStaged:
		if !added && head != "" {
			before = gdBlobRef{git: head + ":" + old}
		}
		if !deleted {
			after = gdBlobRef{git: ":0:" + cf.Path}
		}
	default:
		if !added && head != "" {
			before = gdBlobRef{git: head + ":" + old}
		}
		if !deleted {
			after = worktree
		}
	}
	return before, after
}

// gdReadMediaSide reads one side: its size always, its picture when the file
// is one and fits the cap. Runs off the UI lane; the image is wrapped on it.
func gdReadMediaSide(runner *gitworkbench.Runner, root string, ref gdBlobRef, picture bool) (gdMediaSide, image.Image) {
	var side gdMediaSide
	var raw []byte
	ctx, cancel := context.WithTimeout(context.Background(), gdMediaTimeout)
	defer cancel()
	switch {
	case ref.git != "":
		size, err := runner.BlobSize(ctx, root, ref.git)
		if err != nil {
			return side, nil
		}
		side.present, side.size = true, size
		if picture && size <= gdMediaMaxBytes {
			raw, _ = runner.ReadBlob(ctx, root, ref.git, gdMediaMaxBytes)
		}
	case ref.file != "":
		info, err := os.Lstat(ref.file)
		if err != nil || !info.Mode().IsRegular() {
			return side, nil
		}
		side.present, side.size = true, info.Size()
		if picture && side.size <= gdMediaMaxBytes {
			raw, _ = os.ReadFile(ref.file)
		}
	default:
		return side, nil
	}
	if !picture {
		return side, nil
	}
	if side.size > gdMediaMaxBytes {
		side.note = "Too large to preview"
		return side, nil
	}
	img, err := filesview.DecodeImageBytes(raw)
	switch {
	case errors.Is(err, filesview.ErrDimensionsTooLarge):
		side.note = "Dimensions too large to preview"
	case err != nil:
		side.note = "Can't preview this image"
	default:
		b := img.Bounds()
		side.w, side.h = b.Dx(), b.Dy()
		return side, img
	}
	return side, nil
}

// gdLoadMedia reads both sides of a binary file off the UI lane. The file
// keeps showing what it had until the new answer lands.
func (s *Shell) gdLoadMedia(f *gdFile) {
	snap := s.gdActiveSnapshot()
	if s.git == nil || s.git.root == "" || snap == nil || f.media.state == gdMediaLoading {
		return
	}
	f.media.state = gdMediaLoading
	f.media.image = nativeImageDecodable(strings.ToLower(filepath.Ext(f.cf.Path)))
	before, after := s.gdMediaRefs(f, snap)
	runner, root, files, picture := s.git.runner, s.git.root, s.git.gdFilesFor, f.media.image
	load := func() (b, a gdMediaSide, bi, ai image.Image) {
		b, bi = gdReadMediaSide(runner, root, before, picture)
		a, ai = gdReadMediaSide(runner, root, after, picture)
		return
	}
	apply := func(b, a gdMediaSide, bi, ai image.Image) {
		// A refresh that replaced the files has dropped this one with them.
		if s.git == nil || s.git.gdFilesFor != files {
			return
		}
		// The Bitmap wrap needs the GPU, so a headless run keeps the facts only.
		if s.win != nil {
			if bi != nil {
				b.bmp = ui.NewBitmap(bi)
			}
			if ai != nil {
				a.bmp = ui.NewBitmap(ai)
			}
		}
		f.media.before, f.media.after, f.media.state = b, a, gdMediaReady
		s.git.gdRowsDirty = true
	}
	if s.win == nil {
		b, a, bi, ai := load()
		apply(b, a, bi, ai)
		return
	}
	go func() {
		b, a, bi, ai := load()
		s.applyOnUI(func() { apply(b, a, bi, ai) })
	}()
}

// gdMediaRow shows a binary file's change: its pictures side by side, or for
// anything else the size it changed from and to.
func (s *Shell) gdMediaRow(c *ui.Context, pal *gdPalette, f *gdFile) {
	if f.media.state == gdMediaIdle {
		s.gdLoadMedia(f)
	}
	m := &f.media
	typ := Typography()
	if m.state != gdMediaReady && !m.before.present && !m.after.present {
		s.gdCard(c, pal).Padding(10, 16).Background(pal.gapBg).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Spinner(c).Size(14, 14).Shrink(0)
			ui.Text(c, "Loading preview…").FontSize(typ.Body).TextColor(pal.gapText)
		})
		return
	}
	if !m.image || !(m.before.present || m.after.present) {
		s.gdCard(c, pal).Padding(10, 16).Background(pal.gapBg).AlignItems(ui.Center).Children(func() {
			ui.Text(c, gdBinarySummary(f.cf, m)).FontSize(typ.Body).TextColor(pal.gapText).Grow(1).MinWidth(0)
		})
		return
	}
	beforeLabel, afterLabel := "Before", "After"
	switch f.cf.Status {
	case gitworkbench.StatusAdded, gitworkbench.StatusUntracked:
		afterLabel = "Added"
	case gitworkbench.StatusDeleted:
		beforeLabel = "Deleted"
	}
	sides := 0
	for _, side := range []gdMediaSide{m.before, m.after} {
		if side.present {
			sides++
		}
	}
	const pad, gap = 16, 16
	room := (s.gdCardWidth(c) - 2*pad - float32(sides-1)*gap) / float32(sides)
	s.gdCard(c, pal).Padding(14, pad).Gap(gap).AlignItems(ui.Start).Children(func() {
		if m.before.present {
			s.gdMediaSideView(c, pal, beforeLabel, pal.delText, &m.before, room)
		}
		if m.after.present {
			s.gdMediaSideView(c, pal, afterLabel, pal.addText, &m.after, room)
		}
	})
}

// gdMediaSideView draws one side: its label and size line, then its picture
// scaled down to the room, or why there is none.
func (s *Shell) gdMediaSideView(c *ui.Context, pal *gdPalette, label string, color ui.Color, side *gdMediaSide, room float32) {
	typ := Typography()
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(6).AlignItems(ui.Start).Children(func() {
		caption := filesview.FormatFileSize(side.size)
		if side.w > 0 {
			caption = itoa(side.w) + " × " + itoa(side.h) + " · " + caption
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(typ.BodySmall).FontWeight(WeightSemibold).TextColor(color)
			ui.Text(c, caption).FontSize(typ.BodySmall).TextColor(pal.gapText).SingleLine()
		})
		switch {
		case side.bmp != nil:
			scale := min(1, room/float32(side.w), gdMediaMaxH/float32(side.h))
			ui.Box(c).Border(1, pal.cardBorder).Radius(4).Children(func() {
				ui.Image(c, side.bmp).Size(float32(side.w)*scale, float32(side.h)*scale)
			})
		case side.note != "":
			ui.Text(c, side.note).FontSize(typ.Body).TextColor(pal.gapText)
		}
	})
}

// gdBinarySummary is the one line for a binary file that is not a picture,
// or whose picture could not be read.
func gdBinarySummary(cf *gitworkbench.ChangeFile, m *gdMedia) string {
	switch {
	case m.before.present && m.after.present:
		return "Binary file · " + filesview.FormatFileSize(m.before.size) + " → " + filesview.FormatFileSize(m.after.size)
	case m.after.present:
		return "Binary file · added, " + filesview.FormatFileSize(m.after.size)
	case m.before.present:
		return "Binary file · deleted, " + filesview.FormatFileSize(m.before.size)
	}
	return "Binary file changed."
}
