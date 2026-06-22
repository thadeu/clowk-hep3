package sip

import "testing"

func TestParse_INVITE(t *testing.T) {
	raw := "INVITE sip:551131816174@sip.example.com SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 10.0.0.1;branch=z9hG4bK1\r\n" +
		"From: \"Alice\" <sip:alice@example.com>;tag=abc\r\n" +
		"To: <sip:551131816174@example.com>\r\n" +
		"Call-ID: call-12345@10.0.0.1\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"User-Agent: FreeSWITCH-mod_sofia\r\n" +
		"X-CID: correlation-999\r\n" +
		"Content-Type: application/sdp\r\n" +
		"\r\n" +
		"v=0\r\no=...\r\n" // body must be ignored

	m := Parse(raw, "X-CID")

	if !m.IsRequest {
		t.Fatal("IsRequest = false, want true")
	}

	if m.Method != "INVITE" {
		t.Errorf("Method = %q, want INVITE", m.Method)
	}

	if m.RURI != "sip:551131816174@sip.example.com" {
		t.Errorf("RURI = %q", m.RURI)
	}

	if m.CallID != "call-12345@10.0.0.1" {
		t.Errorf("CallID = %q", m.CallID)
	}

	if m.FromUser != "alice" {
		t.Errorf("FromUser = %q, want alice", m.FromUser)
	}

	if m.ToUser != "551131816174" {
		t.Errorf("ToUser = %q, want 551131816174", m.ToUser)
	}

	if m.CSeq != "1 INVITE" {
		t.Errorf("CSeq = %q", m.CSeq)
	}

	if m.UserAgent != "FreeSWITCH-mod_sofia" {
		t.Errorf("UserAgent = %q", m.UserAgent)
	}

	if m.Correlation != "correlation-999" {
		t.Errorf("Correlation = %q, want correlation-999", m.Correlation)
	}
}

func TestParse_Response(t *testing.T) {
	raw := "SIP/2.0 200 OK\r\n" +
		"Call-ID: call-12345\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"\r\n"

	m := Parse(raw, "")

	if m.IsRequest {
		t.Error("IsRequest = true for a response")
	}

	if m.ResponseCode != 200 {
		t.Errorf("ResponseCode = %d, want 200", m.ResponseCode)
	}

	if m.Method != "" {
		t.Errorf("Method = %q, want empty on a response", m.Method)
	}
}

func TestParse_FailureResponse(t *testing.T) {
	m := Parse("SIP/2.0 486 Busy Here\r\nCall-ID: x\r\n\r\n", "")

	if m.ResponseCode != 486 {
		t.Errorf("ResponseCode = %d, want 486", m.ResponseCode)
	}
}

// Compact header forms (RFC 3261 §20): i=Call-ID, f=From, t=To.
func TestParse_CompactHeaders(t *testing.T) {
	raw := "INVITE sip:bob@x SIP/2.0\r\n" +
		"f: <sip:carol@x>;tag=1\r\n" +
		"t: <sip:dave@x>\r\n" +
		"i: compact-callid\r\n" +
		"\r\n"

	m := Parse(raw, "")

	if m.CallID != "compact-callid" {
		t.Errorf("CallID = %q (compact i:)", m.CallID)
	}

	if m.FromUser != "carol" {
		t.Errorf("FromUser = %q (compact f:)", m.FromUser)
	}

	if m.ToUser != "dave" {
		t.Errorf("ToUser = %q (compact t:)", m.ToUser)
	}
}

func TestParse_CorrelationHeaderCaseInsensitive(t *testing.T) {
	raw := "INVITE sip:x SIP/2.0\r\nCall-ID: c\r\nx-cid: lower-case-hdr\r\n\r\n"

	m := Parse(raw, "X-CID")

	if m.Correlation != "lower-case-hdr" {
		t.Errorf("Correlation = %q, want lower-case-hdr (header match must be case-insensitive)", m.Correlation)
	}
}

func TestParse_EmptyCorrelationHeaderSkips(t *testing.T) {
	raw := "INVITE sip:x SIP/2.0\r\nCall-ID: c\r\nX-CID: should-not-capture\r\n\r\n"

	m := Parse(raw, "")

	if m.Correlation != "" {
		t.Errorf("Correlation = %q, want empty when no header configured", m.Correlation)
	}
}

func TestExtractUser(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`"Alice" <sip:alice@example.com>;tag=1928`, "alice"},
		{`<sip:bob@example.com>`, "bob"},
		{`sip:carol@example.com;tag=1`, "carol"},
		{`sips:dave@secure.example.com`, "dave"},
		{`<sip:example.com>`, ""}, // host-only, no user
		{`tel:+15551234567`, ""},  // non-sip scheme
		{`<sip:+5511999@gw>;user=phone`, "+5511999"},
	}

	for _, tt := range tests {
		if got := extractUser(tt.in); got != tt.want {
			t.Errorf("extractUser(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A lone-LF capture (no CR) must still parse — receivers vary.
func TestParse_LoneLF(t *testing.T) {
	m := Parse("INVITE sip:x SIP/2.0\nCall-ID: c\n\n", "")

	if m.Method != "INVITE" || m.CallID != "c" {
		t.Errorf("lone-LF parse failed: method=%q callid=%q", m.Method, m.CallID)
	}
}
