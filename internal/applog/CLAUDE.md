# internal/applog/
> L2 | Parent: ../CLAUDE.md

applog.go: package contract and setup for bounded structured application logging.
rolling.go: size-bounded rotating log writer.
rolling_test.go: rotation and bounded-write regressions.
stderr_unix.go: macOS POSIX fd-2 stderr capture; constrained to `unix && !linux`.
stderr_other.go: no-op `CaptureStderr` for Windows and Linux, so release builds stay cross-platform.
stderr_test.go: fd-level capture regressions; constrained beside the POSIX implementation.
CLAUDE.md: this file.

[PROTOCOL]: Update this header on change, then check CLAUDE.md.
