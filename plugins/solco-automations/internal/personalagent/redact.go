package personalagent

import (
	"regexp"
	"strings"
)

// Redact removes what must never reach a log, a notice or a stored error line:
// the run's own prompt/brief text (when an error echoes it) and anything shaped
// like a credential. The original error is the Agent's or Herdr's; this is the
// one place Solco decides what of it may travel on.
var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
}

// Redact returns text with each of `own` (the prompt or brief the caller
// holds) and every credential-shaped run replaced.
func Redact(text string, own ...string) string {
	for _, o := range own {
		if o = strings.TrimSpace(o); len(o) >= 8 {
			text = strings.ReplaceAll(text, o, "[redacted]")
		}
	}
	for _, re := range secretShapes {
		text = re.ReplaceAllString(text, "[redacted]")
	}
	return text
}
