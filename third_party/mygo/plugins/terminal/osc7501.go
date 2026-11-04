package terminal

// The OSC 7501 scanner is framing-only. Program Status record semantics
// belong to the embedding Solco application. It runs beside libghostty-vt,
// which ignores 7501, on exactly the same terminal output stream.

const osc7501Reply = "\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\"
const osc7501Max = 4096

const (
	oscGround = iota
	oscAfterESC
	oscNum
	oscSkip
	oscBody
	oscBodyESC
	oscPromptBody
	oscPromptESC
)

type osc7501EventKind uint8

const (
	osc7501Report osc7501EventKind = iota + 1
	osc7501Query
	osc7501Prompt // OSC 133;A: next shell prompt
	osc7501Reset  // RIS, ESC c; not soft reset DECSTR
)

type osc7501Event struct {
	kind osc7501EventKind
	body []byte
}

// osc7501Scanner reassembles split sequences, preserving event order
// across multiple reports, feature queries, prompts and resets per chunk.
// Its buffers are bounded. No text of a report ever enters a reply.
type osc7501Scanner struct {
	state int
	num   []byte
	body  []byte
}

// feed is the historical parser-test API. Production uses feedEvents so
// multiple queries and lifecycle controls are never coalesced or reordered.
func (s *osc7501Scanner) feed(p []byte) (bodies [][]byte, query bool) {
	for _, event := range s.feedEvents(p) {
		switch event.kind {
		case osc7501Report:
			bodies = append(bodies, event.body)
		case osc7501Query:
			query = true
		}
	}
	return bodies, query
}

func (s *osc7501Scanner) feedEvents(p []byte) (events []osc7501Event) {
	for _, c := range p {
		switch s.state {
		case oscGround:
			if c == 0x1b {
				s.state = oscAfterESC
			}
		case oscAfterESC:
			switch c {
			case 0x1b:
				// Keep looking for the next escape introducer.
			case ']':
				s.state = oscNum
				s.num = s.num[:0]
			case 'c':
				events = append(events, osc7501Event{kind: osc7501Reset})
				s.state = oscGround
			default:
				s.state = oscGround
			}
		case oscNum:
			switch {
			case c >= '0' && c <= '9':
				if len(s.num) < 8 {
					s.num = append(s.num, c)
				} else {
					s.state = oscSkip
				}
			case c == ';' && string(s.num) == "7501":
				s.state = oscBody
				s.body = s.body[:0]
			case c == ';' && string(s.num) == "133":
				s.state = oscPromptBody
				s.body = s.body[:0]
			case c == ';':
				s.state = oscSkip
			case c == 0x07:
				s.state = oscGround
			case c == 0x1b:
				s.state = oscAfterESC
			default:
				s.state = oscSkip
			}
		case oscSkip:
			switch c {
			case 0x07:
				s.state = oscGround
			case 0x1b:
				s.state = oscAfterESC
			}
		case oscBody, oscPromptBody:
			switch c {
			case 0x07:
				events = s.emit(events, 1)
			case 0x1b:
				if s.state == oscBody {
					s.state = oscBodyESC
				} else {
					s.state = oscPromptESC
				}
			default:
				// 7-byte OSC 7501 prefix + 1-byte BEL is the smallest
				// complete framing. ST is accounted for at emission.
				limit := osc7501Max - len("\x1b]7501;") - 1
				if s.state == oscPromptBody {
					limit = 64 // Only the OSC 133;A tag is needed.
				}
				if len(s.body) < limit {
					s.body = append(s.body, c)
				} else {
					s.state = oscSkip
				}
			}
		case oscBodyESC, oscPromptESC:
			switch c {
			case '\\':
				events = s.emit(events, 2)
			case 0x1b:
				// The previous OSC was canceled. A second ESC can
				// start a new sequence on the next byte.
				s.state = oscAfterESC
			case ']':
				// The previous OSC was canceled by a new OSC.
				s.state = oscNum
				s.num = s.num[:0]
			case 'c':
				// RIS cancels the old OSC and resets status.
				events = append(events, osc7501Event{kind: osc7501Reset})
				s.state = oscGround
			default:
				s.state = oscGround
			}
		}
	}
	return events
}

func (s *osc7501Scanner) emit(events []osc7501Event, terminatorLen int) []osc7501Event {
	origin := s.state
	s.state = oscGround
	if origin == oscPromptBody || origin == oscPromptESC {
		if len(s.body) == 1 && s.body[0] == 'A' {
			return append(events, osc7501Event{kind: osc7501Prompt})
		}
		return events
	}
	if len(s.body)+len("\x1b]7501;")+terminatorLen > osc7501Max {
		return events
	}
	if len(s.body) == 1 && s.body[0] == '?' {
		return append(events, osc7501Event{kind: osc7501Query})
	}
	if len(s.body) > 0 {
		return append(events, osc7501Event{kind: osc7501Report, body: append([]byte(nil), s.body...)})
	}
	return events
}
