package personalagent

/**
 * [INPUT]: 旧 herdr-projects 数据目录（默认 ~/.herdr-projects，HERDR_PROJECTS_HOME 可改）与一个目标目录
 * [OUTPUT]: LegacyHome、ExportLegacy（只读复制成 <dst>/herdr-projects-<时间戳>/）、`assistant legacy-export` 命令
 * [POS]: SOLCO-D03（2026-11-03，E06）：旧 herdr-projects 的数据只在用户显式导出时被**读取**——复制到新文件夹，从不写、不删、不移动源目录；
 *        卸载旧插件或移除 Solco 里的旧集成都不会触碰它；符号链接不跟随（只记名字），套接字/设备文件跳过；拒绝把导出放进源目录内
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LegacyHome is where the retired plugin kept its data.
func LegacyHome() string {
	if p := os.Getenv("HERDR_PROJECTS_HOME"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".herdr-projects")
}

// LegacyExport is what one export copied.
type LegacyExport struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Files   int    `json:"files"`
	Bytes   int64  `json:"bytes"`
	Skipped int    `json:"skipped"`
}

// ExportLegacy copies src into a new folder under dst. It reads src and nothing else: the source is never
// written, renamed or removed, and a symlink is not followed.
func ExportLegacy(src, dst string, now time.Time) (LegacyExport, error) {
	out := LegacyExport{From: src}
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return out, coded("not_found", "there is no herdr-projects data to export")
		}
		return out, err
	}
	if !info.IsDir() {
		return out, coded("invalid", "the herdr-projects data path is not a folder")
	}
	absSrc, _ := filepath.Abs(src)
	absDst, _ := filepath.Abs(dst)
	if absDst == absSrc || strings.HasPrefix(absDst+string(os.PathSeparator), absSrc+string(os.PathSeparator)) {
		return out, coded("invalid", "the export folder must be outside the herdr-projects data")
	}
	target := filepath.Join(dst, "herdr-projects-"+now.UTC().Format("20060102-150405"))
	if err := os.MkdirAll(target, 0o700); err != nil {
		return out, err
	}
	out.To = target
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			out.Skipped++
			return nil
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		to := filepath.Join(target, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case d.Type()&fs.ModeSymlink != 0, !d.Type().IsRegular():
			out.Skipped++
			return nil
		}
		n, err := copyFile(path, to)
		if err != nil {
			out.Skipped++
			return nil
		}
		out.Files++
		out.Bytes += n
		return nil
	})
	return out, err
}

func copyFile(from, to string) (int64, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	f, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, in)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}
