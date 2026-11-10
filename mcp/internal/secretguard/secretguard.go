// Package secretguard replaces credential-shaped values in capture text with
// a secret:// reference before the capture is stored. Captures are immutable,
// so a credential that reaches the queue cannot be edited out afterwards.
//
// Detection is deterministic: the same input always produces the same output,
// which keeps a retried capture identical to its first attempt. It is a
// reduction in risk, not a guarantee. A secret with no recognizable shape, such
// as a short human-chosen password in running prose, passes through.
package secretguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Placeholder is the reference written in place of a removed value, followed
// by the kind of value it replaced.
const Placeholder = "secret://redacted/"

const (
	kindAssigned = "assigned-secret"
	kindEntropy  = "high-entropy-value"
	// minRandomEntropy is in bits per character. Long camel-case and dated
	// identifiers measure 4.1 to 4.6; random base62 of 48 characters or more
	// measures above 4.7 in more than nine cases out of ten.
	minRandomEntropy = 4.7
)

// ErrInvalidJSON reports input that is not one well-formed JSON value, or a
// rewrite that would not be.
var ErrInvalidJSON = errors.New("secretguard: invalid JSON")

// Finding names a kind of removed value and how many were removed. It never
// carries the value.
type Finding struct {
	Kind  string
	Count int
}

// Scan accumulates findings across the fields of one capture.
type Scan struct {
	counts map[string]int
}

// Findings returns the removed kinds in a stable order, or nil when nothing
// was removed.
func (s *Scan) Findings() []Finding {
	if len(s.counts) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(s.counts))
	for kind, count := range s.counts {
		out = append(out, Finding{Kind: kind, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func (s *Scan) note(kind string) {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[kind]++
}

type rule struct {
	kind   string
	re     *regexp.Regexp
	group  int
	accept func(value, text string, start int) bool
}

const sensitiveName = `(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)`

// Rules run in order, most specific first, so a value is labeled by its real
// kind before the generic assignment and entropy rules can claim it.
var rules = []rule{
	{kind: "private-key", re: regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)},
	{kind: "aws-access-key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "github-token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{kind: "api-key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	{kind: "slack-token", re: regexp.MustCompile(`\bxox[abeprs]-[A-Za-z0-9-]{10,}`)},
	{kind: "stripe-key", re: regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`)},
	{kind: "google-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{kind: "npm-token", re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}`)},
	{kind: "tailscale-key", re: regexp.MustCompile(`\btskey-[a-z]+-[A-Za-z0-9-]{16,}`)},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{kind: "authorization-header", group: 1, re: regexp.MustCompile(`(?i)\bauthorization["']?\s*[:=]\s*["']?(?:basic|bearer|token)\s+([^\s"']{8,})`)},
	{kind: "bearer-token", group: 1, accept: credentialLike, re: regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/=-]{20,})`)},
	{kind: "url-credential", group: 1, accept: notPlaceholder, re: regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]*:([^\s/@]+)@`)},
	{kind: kindAssigned, group: 1, accept: credentialLike, re: regexp.MustCompile(`(?i)` + sensitiveName + `(?:[_.-][A-Za-z0-9_.-]*)?["']?\s*[:=]\s*["']?([^\s"',;]{8,})`)},
	{kind: kindAssigned, group: 1, accept: credentialLike, re: regexp.MustCompile(`(?i)--(?:[a-z0-9-]*-)?(?:password|passwd|secret|token|api-key)\s+([^\s"']{8,})`)},
	{kind: kindEntropy, accept: randomLooking, re: regexp.MustCompile(`[A-Za-z0-9+=_-]{40,}`)},
}

var (
	sensitiveKey = regexp.MustCompile(`(?i)` + sensitiveName + `(?:$|[_.-])`)
	hashLabel    = regexp.MustCompile(`(?i)^(?:sha\d+|md5)[:=-]`)
)

// Text returns s with credential-shaped values replaced.
func (s *Scan) Text(text string) string {
	for i := range rules {
		text = s.apply(text, &rules[i])
	}
	return text
}

func (s *Scan) apply(text string, r *rule) string {
	matches := r.re.FindAllStringSubmatchIndex(text, -1)
	if matches == nil {
		return text
	}
	var out strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[2*r.group], m[2*r.group+1]
		if start < last {
			continue
		}
		value := text[start:end]
		if strings.HasPrefix(value, "secret://") || (r.accept != nil && !r.accept(value, text, start)) {
			continue
		}
		out.WriteString(text[last:start])
		out.WriteString(Placeholder + r.kind)
		last = end
		s.note(r.kind)
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

// JSON returns raw with credential-shaped string values replaced. Object keys,
// numbers, key order and whitespace are copied through untouched, so exact
// JSON numbers and unknown fields survive.
func (s *Scan) JSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, ErrInvalidJSON
	}
	out := make([]byte, 0, len(raw))
	key, keyed, changed := "", false, false
	for i := 0; i < len(raw); {
		c := raw[i]
		if c != '"' {
			if c != ':' && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				keyed = false
			}
			out = append(out, c)
			i++
			continue
		}
		end := stringEnd(raw, i)
		if end < 0 {
			return nil, ErrInvalidJSON
		}
		literal := raw[i:end]
		var value string
		if json.Unmarshal(literal, &value) != nil {
			return nil, ErrInvalidJSON
		}
		i = end
		if next := skipSpace(raw, end); next < len(raw) && raw[next] == ':' {
			key, keyed = value, true
			out = append(out, literal...)
			continue
		}
		var replaced string
		if keyed && sensitiveKey.MatchString(key) && keyedSecret(value) {
			replaced = Placeholder + kindAssigned
			s.note(kindAssigned)
		} else {
			replaced = s.Text(value)
		}
		keyed = false
		if replaced == value {
			out = append(out, literal...)
			continue
		}
		encoded, err := encodeString(replaced)
		if err != nil {
			return nil, ErrInvalidJSON
		}
		out = append(out, encoded...)
		changed = true
	}
	if !changed {
		return raw, nil
	}
	if !json.Valid(out) {
		return nil, ErrInvalidJSON
	}
	return out, nil
}

func stringEnd(raw []byte, start int) int {
	for i := start + 1; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

func skipSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	return i
}

func encodeString(value string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// notPlaceholder rejects template slots, variable references and paths, which
// name where a secret lives without being one.
func notPlaceholder(value, _ string, _ int) bool {
	return !strings.ContainsRune("$<{[/~.%*", rune(value[0]))
}

// credentialLike separates a generated credential from an ordinary word that
// happens to follow a name such as "token:". Prose, versions, dates and
// upper-case placeholders such as YOUR_API_KEY do not qualify.
func credentialLike(value, text string, start int) bool {
	if !notPlaceholder(value, text, start) {
		return false
	}
	var letters, digits, upper, lower, symbols int
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsUpper(r):
			upper++
			letters++
		case unicode.IsLetter(r):
			lower++
			letters++
		case r != '-' && r != '_' && r != '.' && r != ':':
			symbols++
		}
	}
	if letters == 0 {
		return false
	}
	return digits > 0 || symbols > 0 || (upper >= 2 && lower >= 2)
}

// keyedSecret judges a JSON value whose own key names it a secret. The key is
// strong evidence, so the value only has to be a single token.
func keyedSecret(value string) bool {
	if len(value) < 6 || strings.HasPrefix(value, "secret://") || !notPlaceholder(value, "", 0) {
		return false
	}
	letters := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			return false
		}
		if unicode.IsLetter(r) {
			letters = true
		}
	}
	return letters
}

// randomLooking is the conservative catch-all for long generated values with
// no known format. Hex digests, labeled hashes and public keys are identifiers,
// not secrets, and are left alone.
func randomLooking(value, text string, start int) bool {
	var upper, lower, digit, hex bool
	hex = true
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z':
			lower = true
			if r > 'f' {
				hex = false
			}
		case r >= 'A' && r <= 'Z':
			upper = true
			if r > 'F' {
				hex = false
			}
		default:
			hex = false
		}
	}
	if hex || !upper || !lower || !digit {
		return false
	}
	if strings.HasPrefix(value, "AAAA") {
		return false
	}
	// A digest is labeled at the start of its word, as in SHA256:... or
	// sha256-..., and base64 digests contain slashes that split the match.
	word := start
	for word > 0 && !strings.ContainsRune(" \t\r\n\"'`(=,", rune(text[word-1])) {
		word--
	}
	if hashLabel.MatchString(text[word:start] + value) {
		return false
	}
	// Hyphenated names and dated identifiers reach the entropy of short random
	// strings, so several separators mark a name, and the threshold sits above
	// what mixed-case identifiers measure.
	if strings.Count(value, "-")+strings.Count(value, "_") >= 3 {
		return false
	}
	return entropy(value) >= minRandomEntropy
}

func entropy(value string) float64 {
	var counts [256]int
	for i := 0; i < len(value); i++ {
		counts[value[i]]++
	}
	total := float64(len(value))
	bits := 0.0
	for _, count := range counts {
		if count == 0 {
			continue
		}
		p := float64(count) / total
		bits -= p * math.Log2(p)
	}
	return bits
}
