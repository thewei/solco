//go:build windows || linux

package applog

// CaptureStderr is unavailable on Windows and Linux: Go's syscall package
// either lacks the POSIX redirect or arm64 Linux lacks Dup2. Returning a
// no-op keeps the app buildable while preserving the macOS diagnostic path.
func CaptureStderr(dir string) (stop func(), err error) {
	return func() {}, nil
}
