package nativeui

import (
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: 提交作者名、主题色与 TileTone 调色板
 * [OUTPUT]: authorInitial/authorTone（作者名 → 首字母/稳定色，纯函数）、authorAvatar（提交列表作者列的圆形字母头像）、gitCommitColumnHead（提交列表的列头：Subject / Author / Commit / Date，与行同宽同间距）
 * [POS]: Commits 列表的呈现小件；不联网取头像（无 Gravatar，邮箱不出本机），颜色只由名字散列决定，同一作者在任何视图同色
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// avatarTones are the tile colours an author may wear: no gray (reads as "no
// author") and no yellow (white initials are illegible on it).
var avatarTones = [...]TileTone{TileBlue, TileGreen, TileOrange, TileRed, TilePink, TilePurple, TileTeal, TileIndigo}

// authorTone is the stable colour of an author name: the same name always
// gets the same tile, case and surrounding space aside.
func authorTone(name string) TileTone {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(name))))
	return avatarTones[h.Sum32()%uint32(len(avatarTones))]
}

// authorInitial is the first letter or digit of the name, upper-cased; "?"
// for a name without one.
func authorInitial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// authorAvatar draws the author's round initial tile.
func authorAvatar(c *ui.Context, name string, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).
		Background(authorTone(name).Color(c.Theme().Dark)).Center().Children(func() {
		ui.Text(c, authorInitial(name)).FontSize(size * 0.62).FontWeight(WeightSemibold).
			TextColor(ui.Hex("#ffffff")).PassThrough()
	})
}

// The Commits list's right-hand columns. Rows and the column head read the
// same numbers, so a label always sits over its values.
const (
	commitAuthorColW float32 = 90
	commitHashColW   float32 = 64
	commitDateColW   float32 = 62
	commitAvatarSize float32 = 16
	commitRowGap     float32 = 8
	commitRowInset   float32 = 16 // list padding 8 + row padding 8
)

// gitCommitColumnHead labels the list's columns. The author and date columns
// drop at the same widths as the rows' (gitCommitsShowAuthor/Age).
func (s *Shell) gitCommitColumnHead(c *ui.Context) {
	t := c.Theme()
	windowW, _ := c.Size()
	centerW := s.gitCenterWidth(windowW)
	label := func(text string, w float32) {
		ui.Text(c, text).FontSize(Typography().Micro).FontWeight(WeightMedium).TextColor(t.TextMuted).
			Width(w).SingleLine()
	}
	ui.Column(c).FillWidth().Shrink(0).Children(func() {
		ui.Row(c).FillWidth().Height(22).Padding(0, commitRowInset).Gap(commitRowGap).AlignItems(ui.Center).Children(func() {
			// The graph and chips share the subject's column: the head names
			// the whole stretch before the author.
			ui.Text(c, "Subject").FontSize(Typography().Micro).FontWeight(WeightMedium).TextColor(t.TextMuted).
				Grow(1).MinWidth(0).SingleLine()
			if centerW >= gitCommitsShowAuthor {
				label("Author", commitAuthorColW)
			}
			label("Commit", commitHashColW)
			if centerW >= gitCommitsShowAge {
				label("Date", commitDateColW)
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6)).PassThrough()
	})
}
