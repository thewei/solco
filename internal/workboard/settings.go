package workboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 client.go 的 Exec，插件 CLI 的 `show --json [--repo R | --project-coordinator R]`（{settings[{name,kind,value,from,place,own,note}],problems[]}，1.2.26 沙盒实测）与 `set <name> <value> [scope]` / `unset <name> [scope]`
 * [OUTPUT]: 对外提供 Setting（ValueText/IsBool）、SettingsView、Scope、Client.Settings/SetSetting/UnsetSetting
 * [POS]: 插件设置的原生读写面（用户 2026-10-26 显式授权，越过 AGENTS.md「禁止插件 UI」条款，仅限本插件的设置）：设置的名字、取值范围、校验与「什么范围能设什么」全归插件——JSON 不带选项表，所以本包不复制任何取值集合，插件的拒绝原文透传；不同范围返回的设置集不同（everywhere 79 项含 board，--repo 42 项含 repo 类，--project-coordinator 22 项），UI 只画返回的那份；值以 `--` 开头一律拒绝，不让它被解析成选项
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Setting is one setting as the plugin shows it in a scope.
type Setting struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // work | coordinator | board | repo
	Value any    `json:"value"`
	From  string `json:"from"`  // where the value comes from: "built in", "you", "your entry for repoA"
	Place string `json:"place"` // builtin | you | repo | coord | …
	Own   bool   `json:"own"`   // set in this very scope, so it can be unset
	Note  string `json:"note"`  // the plugin's remark, e.g. "does nothing: claude has no fast mode"
}

// IsBool reports a setting the UI shows as a switch.
func (s Setting) IsBool() bool { _, ok := s.Value.(bool); return ok }

// ValueText is the value as the plugin takes it back in `set`.
func (s Setting) ValueText() string {
	switch v := s.Value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	b, _ := json.Marshal(s.Value)
	return string(b)
}

// SettingsView is `show --json`.
type SettingsView struct {
	Settings []Setting
	// Problems are the plugin's own complaints about the settings files, as text.
	Problems []string
}

// Scope says whose settings: everyone's (zero), one repo's, or one repo's
// coordinator's.
type Scope struct {
	Repo        string
	Coordinator bool
}

func (s Scope) args() ([]string, error) {
	if s.Repo == "" {
		if s.Coordinator {
			return nil, errors.New("workboard: settings: a coordinator needs its project")
		}
		return nil, nil
	}
	if strings.HasPrefix(s.Repo, "-") || strings.TrimSpace(s.Repo) == "" {
		return nil, errors.New("workboard: settings: invalid project")
	}
	if s.Coordinator {
		return []string{"--project-coordinator", s.Repo}, nil
	}
	return []string{"--repo", s.Repo}, nil
}

func checkSettingName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n=") {
		return fmt.Errorf("workboard: settings: invalid setting name %q", name)
	}
	return nil
}

// Settings reads the settings of one scope.
func (c *Client) Settings(ctx context.Context, scope Scope) (SettingsView, error) {
	extra, err := scope.args()
	if err != nil {
		return SettingsView{}, err
	}
	out, err := c.exec(ctx, append([]string{"show", "--json"}, extra...)...)
	if err != nil {
		return SettingsView{}, err
	}
	var wire struct {
		Settings []Setting         `json:"settings"`
		Problems []json.RawMessage `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &wire); err != nil {
		return SettingsView{}, fmt.Errorf("workboard: show: %w", err)
	}
	view := SettingsView{}
	for _, s := range wire.Settings {
		if s.Name != "" {
			view.Settings = append(view.Settings, s)
		}
	}
	for _, raw := range wire.Problems {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			text = string(raw)
		}
		if strings.TrimSpace(text) != "" {
			view.Problems = append(view.Problems, text)
		}
	}
	return view, nil
}

// SetSetting writes one setting in a scope and returns what the plugin said
// (the new value, and any remark such as "does nothing: …"). The plugin
// validates the value and refuses what does not fit.
func (c *Client) SetSetting(ctx context.Context, scope Scope, name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if err := checkSettingName(name); err != nil {
		return "", err
	}
	if value == "" || strings.HasPrefix(value, "-") {
		return "", errors.New("workboard: set: invalid value")
	}
	extra, err := scope.args()
	if err != nil {
		return "", err
	}
	return c.exec(ctx, append([]string{"set", name, value}, extra...)...)
}

// UnsetSetting removes the scope's own value, so the one it inherits shows again.
func (c *Client) UnsetSetting(ctx context.Context, scope Scope, name string) (string, error) {
	if err := checkSettingName(name); err != nil {
		return "", err
	}
	extra, err := scope.args()
	if err != nil {
		return "", err
	}
	return c.exec(ctx, append([]string{"unset", name}, extra...)...)
}
