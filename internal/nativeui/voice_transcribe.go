package nativeui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/voice"
)

/**
 * [INPUT]: settings.VoiceSettings 的快照（来源/同意/端点/密钥/模型）、internal/voice.ChatGPT（Codex 登录来源）
 * [OUTPUT]: transcribeClip（一段录音 → 文本）
 * [POS]: 桌面 Chat 页听写与手机 Host 听写（remote_voice.go）共用的唯一转写实现：按设置选 OpenAI 兼容端点（含本地代理）或经同意的 ChatGPT 登录；
 *        只吃设置快照与音频字节，不碰 Shell 状态，所以窗口线程与 Host goroutine 都能调；密钥与音频/文本永不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// transcribeClip sends one recorded clip to the configured source — an
// OpenAI-compatible transcription endpoint (local proxies included) or, when
// the user consented in Settings → Voice, the ChatGPT sign-in Codex holds —
// and returns the text.
func transcribeClip(ctx context.Context, vs settings.VoiceSettings, audio []byte, mime string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("empty or malformed recording")
	}
	if voiceChatGPTOn(vs) {
		return (&voice.ChatGPT{}).Transcribe(ctx, audio, mime)
	}
	endpoint, key, model := vs.EndpointURL, vs.APIKey, vs.Model
	if key == "" {
		key = os.Getenv("OPENAI_API_KEY")
	}
	if endpoint == "" {
		endpoint = voiceDefaultEndpoint
	}
	if model == "" {
		model = voiceDefaultModel
	}
	if mime == "" {
		mime = "audio/webm"
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	ext := "webm" // a phone's recorder may name a type with parameters ("audio/webm;codecs=opus") or none at all
	if _, sub, ok := strings.Cut(mime, "/"); ok {
		if sub, _, _ = strings.Cut(sub, ";"); sub != "" {
			ext = strings.TrimSpace(sub)
		}
	}
	part, err := writer.CreateFormFile("file", "clip."+ext)
	if err == nil {
		part.Write(audio)
		writer.WriteField("model", model)
		writer.WriteField("response_format", "json")
		err = writer.Close()
	}
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(endpoint, "/")+"/audio/transcriptions", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the transcription endpoint: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		detail := string(raw)
		if payload := map[string]any{}; json.Unmarshal(raw, &payload) == nil {
			if inner, ok := payload["error"].(map[string]any); ok {
				if message, ok := inner["message"].(string); ok {
					detail = message
				}
			}
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", errors.New("transcription auth failed — check the voice API key")
		}
		return "", fmt.Errorf("transcription failed (%d): %s", resp.StatusCode, detail)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Text), nil
}
