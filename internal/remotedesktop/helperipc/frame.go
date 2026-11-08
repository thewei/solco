// Package helperipc is the Go end of the link to the native capture/input helper
// (solco-remote-helper): a per-user Unix-domain socket carrying length-prefixed frames, JSON for
// commands/replies/events and a compact binary frame for encoded video — never JSON or Base64 for
// pixels. The wire format is versioned (ProtocolVersion) and pinned byte-for-byte by golden tests
// against the Swift side.
//
//	[u32 big-endian N][u8 kind][N-1 bytes payload]
//
// [INPUT]: net, encoding/json, protocol (DisplayInfo, CaptureConfig, PermissionStatus)
// [OUTPUT]: Server (Listen/Accept), Conn (Call/Send/Video/Events/Done), frame codec, message types, Op* names
// [POS]: transport only — no OS knowledge, no WebRTC; platform/macos supervises the process on top of it
package helperipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// ProtocolVersion must equal the helper's `helperProtocolVersion`.
const ProtocolVersion = 1

// Kind tags a frame.
type Kind byte

const (
	KindControl Kind = 1
	KindVideo   Kind = 2
)

// MaxFrameBytes bounds one frame (kind + payload); larger is corruption.
const MaxFrameBytes = 16 << 20

var (
	ErrFrameTooLarge = errors.New("helperipc: frame too large")
	ErrEmptyFrame    = errors.New("helperipc: empty frame")
	ErrUnknownKind   = errors.New("helperipc: unknown frame kind")
)

// WriteFrame writes one frame in a single Write call.
func WriteFrame(w io.Writer, kind Kind, payload []byte) error {
	if len(payload)+1 > MaxFrameBytes {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)+1))
	buf[4] = byte(kind)
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (Kind, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:4]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[:4])
	if n == 0 {
		return 0, nil, ErrEmptyFrame
	}
	if n > MaxFrameBytes {
		return 0, nil, ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	kind := Kind(body[0])
	if kind != KindControl && kind != KindVideo {
		return 0, nil, fmt.Errorf("%w: %d", ErrUnknownKind, kind)
	}
	return kind, body[1:], nil
}

// VideoFrame is one encoded access unit: Annex-B H.264 with SPS/PPS in-band on keyframes.
type VideoFrame struct {
	CaptureID uint32
	Keyframe  bool
	PTS       time.Duration // since the capture's first frame
	Data      []byte
}

const videoHeaderBytes = 13

const flagKeyframe = 1

// DecodeVideo parses a KindVideo payload.
func DecodeVideo(p []byte) (VideoFrame, error) {
	if len(p) < videoHeaderBytes {
		return VideoFrame{}, errors.New("helperipc: short video payload")
	}
	return VideoFrame{
		CaptureID: binary.BigEndian.Uint32(p),
		Keyframe:  p[4]&flagKeyframe != 0,
		PTS:       time.Duration(binary.BigEndian.Uint64(p[5:13])) * time.Microsecond,
		Data:      p[videoHeaderBytes:],
	}, nil
}

// EncodeVideo is the inverse of DecodeVideo (tests and fakes).
func EncodeVideo(f VideoFrame) []byte {
	p := make([]byte, videoHeaderBytes+len(f.Data))
	binary.BigEndian.PutUint32(p, f.CaptureID)
	if f.Keyframe {
		p[4] = flagKeyframe
	}
	binary.BigEndian.PutUint64(p[5:13], uint64(f.PTS/time.Microsecond))
	copy(p[videoHeaderBytes:], f.Data)
	return p
}
