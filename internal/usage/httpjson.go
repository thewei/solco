package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

/**
 * [INPUT]: 一个 URL、请求头与 context；本机可执行文件名
 * [OUTPUT]: getJSON（状态码 → 统一错误，响应体 1 MiB 上限）、findExecutable（GUI 进程 PATH 之外的常见安装目录）、spanName/humanizeDuration 等窗口命名工具
 * [POS]: 各 Fetcher 的共用 HTTP/进程底座；错误文本只含状态与主机，永不含请求头或 token
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// maxBody bounds one vendor response.
const maxBody = 1 << 20

// fetchTimeout bounds one provider read, network or process.
const fetchTimeout = 20 * time.Second

// newHTTPClient is the client every vendor read uses: bounded, no cookie jar,
// no redirect to another host (a redirect would carry the credential along).
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return http.ErrUseLastResponse
			}
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// getJSON GETs url with headers and decodes a 200 response into out. Other
// statuses map onto the package's failures: 401/403 → ErrUnauthorized,
// 429 → RateLimitedError, anything else → ErrUnavailable with the status.
func getJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, out any) error {
	return requestJSON(ctx, client, http.MethodGet, url, headers, nil, out)
}

// postJSON POSTs a JSON body and decodes a 200 response; failures map as in
// getJSON.
func postJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte, out any) error {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	return requestJSON(ctx, client, http.MethodPost, url, headers, body, out)
}

func requestJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The transport error text embeds the URL; the URL carries no secret
		// here (credentials ride in headers), but keep the text generic.
		return fmt.Errorf("%w: %s unreachable", ErrUnavailable, req.URL.Host)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusTooManyRequests:
		return &RateLimitedError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: %s answered %d", ErrUnavailable, req.URL.Host, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, req.URL.Host)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: unexpected response from %s", ErrUnavailable, req.URL.Host)
	}
	return nil
}

// retryAfter reads a Retry-After header in seconds; 0 when absent or a date.
func retryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// toolDirs are where a GUI app's PATH usually lacks the user's CLIs.
func toolDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".volta", "bin"),
	}
}

// findExecutable resolves a CLI by PATH, then by the usual install dirs.
func findExecutable(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, dir := range toolDirs() {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: %s not found", ErrSetup, name)
}

// toolEnv is the child environment: the app's own plus the install dirs, so
// a node-script CLI finds its interpreter.
func toolEnv(binary string) []string {
	dirs := append([]string{filepath.Dir(binary)}, toolDirs()...)
	return append(os.Environ(), "PATH="+strings.Join(dirs, ":")+":"+os.Getenv("PATH"))
}

// spanName is the display name of a window of length d: "5-hour", "Weekly",
// "Monthly", "3-day".
func spanName(d time.Duration) string {
	switch {
	case d <= 0:
		return "Window"
	case d >= 6*24*time.Hour && d <= 8*24*time.Hour:
		return "Weekly"
	case d >= 27*24*time.Hour && d <= 32*24*time.Hour:
		return "Monthly"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d-day", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("%d-hour", int(d/time.Hour))
	}
	return fmt.Sprintf("%d-minute", int(d/time.Minute))
}
