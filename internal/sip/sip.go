// Package sip extracts the handful of fields clowk-hep3 indexes out of a
// raw SIP message. It is deliberately NOT a full SIP stack: it reads the
// start line and a known set of headers, tolerates compact header forms,
// and never tries to validate the message. A capture viewer keeps the
// raw text anyway, so partial extraction degrades gracefully — a header
// we fail to read just lands empty in the row.
package sip

import (
	"strconv"
	"strings"
)

// Message is the parsed projection of a SIP message. Exactly one of
// IsRequest / response applies: a request carries Method + RURI, a
// response carries ResponseCode (Method/RURI empty).
type Message struct {
	IsRequest    bool
	Method       string
	ResponseCode int
	RURI         string
	CallID       string
	FromUser     string
	ToUser       string
	CSeq         string
	UserAgent    string
	// Correlation is the value of the operator-configured correlation
	// header (e.g. X-CID), used to stitch the two Call-IDs of a B2BUA
	// leg pair into one call. Empty when the header is absent.
	Correlation string
}

// Parse reads a raw SIP message. correlationHeader is the header name
// (case-insensitive, e.g. "X-CID") whose value is captured into
// Message.Correlation; pass "" to skip correlation extraction.
//
// The parser stops at the header/body separator (the first blank line):
// SDP and other bodies are never inspected.
func Parse(raw, correlationHeader string) Message {
	var m Message

	// SIP uses CRLF, but be liberal: split on LF and trim any trailing
	// CR so a lone-LF capture still parses.
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 {
		return m
	}

	parseStartLine(strings.TrimRight(lines[0], "\r"), &m)

	corr := strings.ToLower(strings.TrimSpace(correlationHeader))

	for _, raw := range lines[1:] {
		line := strings.TrimRight(raw, "\r")

		// Blank line → start of the message body. Headers done.
		if line == "" {
			break
		}

		// Continuation line (folded header) — rare in practice and not
		// needed for the fields we index. Skip leading-whitespace lines.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}

		name, value, ok := splitHeader(line)
		if !ok {
			continue
		}

		canon := canonHeader(name)

		switch canon {
		case "call-id":
			if m.CallID == "" {
				m.CallID = value
			}

		case "from":
			if m.FromUser == "" {
				m.FromUser = extractUser(value)
			}

		case "to":
			if m.ToUser == "" {
				m.ToUser = extractUser(value)
			}

		case "cseq":
			if m.CSeq == "" {
				m.CSeq = value
			}

		case "user-agent":
			if m.UserAgent == "" {
				m.UserAgent = value
			}
		}

		if corr != "" && m.Correlation == "" && strings.ToLower(name) == corr {
			m.Correlation = value
		}
	}

	return m
}

// parseStartLine classifies the message and fills Method+RURI (request)
// or ResponseCode (response).
//
//	request:  "INVITE sip:bob@example.com SIP/2.0"
//	response: "SIP/2.0 200 OK"
func parseStartLine(line string, m *Message) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}

	if strings.HasPrefix(fields[0], "SIP/") {
		m.IsRequest = false
		m.ResponseCode, _ = strconv.Atoi(fields[1])

		return
	}

	m.IsRequest = true
	m.Method = fields[0]
	m.RURI = fields[1]
}

// splitHeader splits "Name: value" into its parts, trimming surrounding
// whitespace. Returns ok=false when there is no colon.
func splitHeader(line string) (name, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}

	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

// canonHeader maps a header name (any case, compact or long form) to a
// canonical lowercase long-form key. Compact forms are the single-letter
// aliases defined by RFC 3261 §7.3.3 / §20.
func canonHeader(name string) string {
	lower := strings.ToLower(name)

	switch lower {
	case "i":
		return "call-id"
	case "f":
		return "from"
	case "t":
		return "to"
	case "v":
		return "via"
	case "m":
		return "contact"
	}

	return lower
}

// extractUser pulls the user part out of a From/To header value. It
// handles the common shapes:
//
//	"Alice" <sip:alice@example.com>;tag=1928
//	<sip:alice@example.com>
//	sip:alice@example.com;tag=1928
//	sips:alice@example.com
//
// Returns "" when no sip:/sips: user can be found.
func extractUser(value string) string {
	lower := strings.ToLower(value)

	scheme := "sip:"
	idx := strings.Index(lower, "sip:")

	if sidx := strings.Index(lower, "sips:"); sidx >= 0 && (idx < 0 || sidx < idx) {
		scheme = "sips:"
		idx = sidx
	}

	if idx < 0 {
		return ""
	}

	rest := value[idx+len(scheme):]

	at := strings.IndexByte(rest, '@')
	if at < 0 {
		// No user@host — could be a host-only URI (sip:example.com).
		// No user to extract.
		return ""
	}

	user := rest[:at]

	// A user can itself be quoted/parameterised in pathological cases;
	// cut at the first delimiter that cannot appear in a userinfo token.
	if cut := strings.IndexAny(user, ":;>? "); cut >= 0 {
		user = user[:cut]
	}

	return user
}
