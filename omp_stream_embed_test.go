package main

import (
	"bytes"
	"os"
	"testing"
)

/**
 * [INPUT]: Go-embedded canonical OMP extension and checkout script.
 * [OUTPUT]: build-time package always ships the exact runtime-tested extension.
 * [POS]: SC-05 productization guard; no dual source or checkout dependency at runtime.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestOMPStreamExtensionBundledFromCanonicalSource(t *testing.T) {
	canonical, err := os.ReadFile("scripts/omp-chat-stream.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(embeddedOMPStreamExtension) == 0 || !bytes.Equal(embeddedOMPStreamExtension, canonical) {
		t.Fatal("embedded OMP source differs from the tested Extension")
	}
}
