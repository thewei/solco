package remotehost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

/**
 * [INPUT]: 依赖 Options.Voice（App 注入的转写缝，即桌面 Settings → Voice 所选来源）、server.go 的 guard/writeError
 * [OUTPUT]: 对外提供 VoiceTranscriber 与 POST /api/v3/voice/transcribe（scope terminal-input）、bootstrap 的 capabilities.voice
 * [POS]: 手机听写 = 远程调用桌面的语音功能：手机录一段短音频（base64 JSON），Host 交给桌面配置好的转写来源（自填 OpenAI 兼容端点或经同意的 ChatGPT 登录），回文本；
 *        手机不持有任何凭据，Host 不存音频、不缓存文本；Voice 为 nil 或桌面未配置来源时 bootstrap 不声明 `voice`、路由回 404 voice_unavailable；
 *        音频 ≤ 6 MiB、整次调用 ≤ 90 s；日志只记设备、字节数与耗时，音频与文本永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// maxVoiceAudio bounds one clip (a minute of speech is well under 1 MiB).
const maxVoiceAudio = 6 << 20

// voiceBodyLimit is that audio as base64 plus the JSON envelope.
const voiceBodyLimit = (maxVoiceAudio/3+1)*4 + 8<<10

// voiceTimeout bounds one transcription (the desktop's own sources time out sooner).
const voiceTimeout = 90 * time.Second

// VoiceTranscriber turns one recorded clip into text with the source the user
// chose on the desktop. Ready reports whether any source is usable now.
type VoiceTranscriber interface {
	Ready() bool
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

type voiceRequest struct {
	DataBase64 string `json:"dataBase64"`
	Mime       string `json:"mime,omitempty"`
}

type voiceReply struct {
	Text string `json:"text"`
}

func (s *Server) voiceReady() bool {
	return s.opts.Voice != nil && s.opts.Voice.Ready()
}

func (s *Server) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request, dev Device) {
	if !s.voiceReady() {
		writeError(w, http.StatusNotFound, "voice_unavailable", "")
		return
	}
	var req voiceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, voiceBodyLimit)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	audio, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil || len(audio) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "")
		return
	}
	if len(audio) > maxVoiceAudio {
		writeError(w, http.StatusRequestEntityTooLarge, "audio_too_large", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), voiceTimeout)
	defer cancel()
	started := time.Now()
	text, err := s.opts.Voice.Transcribe(ctx, audio, req.Mime)
	if err != nil {
		slog.Warn("remote host transcribe failed", "device", dev.ID, "bytes", len(audio), "error", err)
		writeError(w, http.StatusBadGateway, "transcribe_failed", truncate(err.Error(), 200))
		return
	}
	slog.Info("remote host transcribed", "device", dev.ID, "bytes", len(audio), "ms", time.Since(started).Milliseconds())
	writeJSON(w, http.StatusOK, voiceReply{Text: text})
}
