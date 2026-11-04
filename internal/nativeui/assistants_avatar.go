package nativeui

import (
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

/**
 * [INPUT]: Assistant/Group 的稳定 ID 与显示名、TileTone 调色板、theme
 * [OUTPUT]: avatarInitial/avatarToneForID（名字 → 首字、稳定 ID → 色，纯函数）、assistantAvatar（圆形首字母头像）、groupAvatar（成员叠头像）、avatarIdentity
 * [POS]: Assistants 侧栏与群聊共用的身份小件（产品决策 2026-11-04，3.1 U0）：个人助理 = 圆形首字，Workspace 仍是方形 letterTile，群 = 叠头像；颜色只由稳定 ID 决定，改名不换色；不联网取图，缺图降级即字母
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// avatarIdentity is one face: the stable id picks the colour, the name the letter.
type avatarIdentity struct{ ID, Name string }

// avatarInitial is the first meaningful letter or digit of a name, upper-cased
// (a Han character stays as it is); a name with none falls back to its first
// visible rune (an emoji), and an empty name to "?".
func avatarInitial(name string) string {
	name = strings.TrimSpace(name)
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	for _, r := range name {
		if !unicode.IsSpace(r) && !unicode.IsControl(r) {
			return string(r)
		}
	}
	return "?"
}

// avatarToneForID gives an id a stable tile tone (never gray, never yellow):
// renaming an Assistant keeps its colour. An empty id falls back to the name.
func avatarToneForID(id, name string) TileTone {
	key := id
	if key == "" {
		key = strings.ToLower(strings.TrimSpace(name))
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return avatarTones[h.Sum32()%uint32(len(avatarTones))]
}

// assistantAvatar draws the Assistant's round initial tile.
func assistantAvatar(c *ui.Context, who avatarIdentity, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).
		Background(avatarToneForID(who.ID, who.Name).Color(c.Theme().Dark)).Center().Children(func() {
		ui.Text(c, avatarInitial(who.Name)).FontSize(size * 0.62).FontWeight(WeightSemibold).
			TextColor(ui.Hex("#ffffff")).PassThrough()
	})
}

// groupAvatarFaces are how many members a Group's stack shows.
const groupAvatarFaces = 3

// groupAvatar stacks up to three members' faces, each a little right of the
// last and ringed in the sidebar's own colour so they read as separate discs.
// A Group with no member to show draws a single neutral disc.
func groupAvatar(c *ui.Context, members []avatarIdentity, size float32) {
	t := c.Theme()
	if len(members) > groupAvatarFaces {
		members = members[:groupAvatarFaces]
	}
	if len(members) == 0 {
		members = []avatarIdentity{{ID: "group", Name: "G"}}
	}
	face := size * 0.72
	step := (size - face) / float32(max(len(members)-1, 1))
	ui.Box(c).Size(size, size).Shrink(0).PassThrough().Children(func() {
		for i, m := range members {
			ui.Box(c.Key("g"+m.ID)).Size(face, face).Shrink(0).Radius(face/2).
				Absolute().Left(float32(i)*step).Top((size-face)/2).
				Background(avatarToneForID(m.ID, m.Name).Color(t.Dark)).
				Border(1, t.Background).Center().Children(func() {
				ui.Text(c, avatarInitial(m.Name)).FontSize(face * 0.6).FontWeight(WeightSemibold).
					TextColor(ui.Hex("#ffffff")).PassThrough()
			})
		}
	})
}
