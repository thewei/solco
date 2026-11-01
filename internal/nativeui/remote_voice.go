package nativeui

import (
	"context"
	"os"

	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/voice"
)

/**
 * [INPUT]: settingsService.Current（锁内设置快照）、voice_transcribe.go 的 transcribeClip、internal/voice.ChatGPT.Status（Codex 登录是否存在）
 * [OUTPUT]: Shell.hostVoice（remotehost.VoiceTranscriber）
 * [POS]: 手机听写与桌面 Chat 页听写同一实现同一设置（Settings → Voice）：Ready = 已同意的 ChatGPT 登录仍在，或有 API 密钥（设置或 OPENAI_API_KEY），或自填了端点（本地代理可无密钥）；
 *        每次调用现读设置快照，桌面改设置立即对手机生效；只碰线程安全面，不碰 Shell 状态，手机不持有凭据
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type remoteVoice struct {
	current func() settings.Settings
}

func (s *Shell) hostVoice() *remoteVoice {
	if s.settingsService == nil {
		return nil
	}
	return &remoteVoice{current: s.settingsService.Current}
}

func (v *remoteVoice) Ready() bool {
	vs := v.current().Voice
	if voiceChatGPTOn(vs) {
		return (&voice.ChatGPT{}).Status().State != voice.SignedOut
	}
	return vs.APIKey != "" || vs.EndpointURL != "" || os.Getenv("OPENAI_API_KEY") != ""
}

func (v *remoteVoice) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	return transcribeClip(ctx, v.current().Voice, audio, mime)
}
