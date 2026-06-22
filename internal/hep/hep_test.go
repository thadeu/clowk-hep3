package hep

import (
	"encoding/binary"
	"testing"
	"time"
)

// chunk builds one generic (vendor 0x0000) HEP3 chunk. The length field
// includes the 6-byte header — the contract we must honour.
func chunk(typ uint16, body []byte) []byte {
	b := make([]byte, 6+len(body))

	binary.BigEndian.PutUint16(b[0:2], 0)
	binary.BigEndian.PutUint16(b[2:4], typ)
	binary.BigEndian.PutUint16(b[4:6], uint16(6+len(body)))
	copy(b[6:], body)

	return b
}

func u8(v uint8) []byte   { return []byte{v} }
func u16(v uint16) []byte { b := make([]byte, 2); binary.BigEndian.PutUint16(b, v); return b }
func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

// buildHEP3 assembles a full HEP3 datagram from chunks.
func buildHEP3(chunks ...[]byte) []byte {
	var body []byte

	for _, c := range chunks {
		body = append(body, c...)
	}

	pkt := make([]byte, 6+len(body))
	copy(pkt[0:4], "HEP3")
	binary.BigEndian.PutUint16(pkt[4:6], uint16(6+len(body)))
	copy(pkt[6:], body)

	return pkt
}

func TestParse_FullSIPPacket(t *testing.T) {
	payload := "INVITE sip:bob@example.com SIP/2.0\r\nCall-ID: abc123\r\n\r\n"

	pkt := buildHEP3(
		chunk(chunkIPFamily, u8(2)),    // AF_INET
		chunk(chunkIPProtocol, u8(17)), // UDP
		chunk(chunkIPv4Src, []byte{1, 2, 3, 4}),
		chunk(chunkIPv4Dst, []byte{5, 6, 7, 8}),
		chunk(chunkSrcPort, u16(5060)),
		chunk(chunkDstPort, u16(5061)),
		chunk(chunkTimeSec, u32(1700000000)),
		chunk(chunkTimeMicro, u32(500000)),
		chunk(chunkProtocolType, u8(ProtocolTypeSIP)),
		chunk(chunkCaptureAgent, u32(100)),
		chunk(chunkCorrelationID, []byte("corr-xyz")),
		chunk(chunkPayload, []byte(payload)),
	)

	p, err := Parse(pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !p.IsSIP() {
		t.Errorf("IsSIP = false, want true (ProtocolType=%d)", p.ProtocolType)
	}

	if p.IPFamily != 2 {
		t.Errorf("IPFamily = %d, want 2", p.IPFamily)
	}

	if p.IPProtocol != 17 {
		t.Errorf("IPProtocol = %d, want 17", p.IPProtocol)
	}

	if got := p.SrcIP.String(); got != "1.2.3.4" {
		t.Errorf("SrcIP = %q, want 1.2.3.4", got)
	}

	if got := p.DstIP.String(); got != "5.6.7.8" {
		t.Errorf("DstIP = %q, want 5.6.7.8", got)
	}

	if p.SrcPort != 5060 {
		t.Errorf("SrcPort = %d, want 5060", p.SrcPort)
	}

	if p.DstPort != 5061 {
		t.Errorf("DstPort = %d, want 5061", p.DstPort)
	}

	wantTS := time.Unix(1700000000, 500000*1000).UTC()
	if !p.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want %v", p.Timestamp, wantTS)
	}

	if p.CaptureAgentID != 100 {
		t.Errorf("CaptureAgentID = %d, want 100", p.CaptureAgentID)
	}

	if p.CorrelationID != "corr-xyz" {
		t.Errorf("CorrelationID = %q, want corr-xyz", p.CorrelationID)
	}

	if string(p.Payload) != payload {
		t.Errorf("Payload = %q, want %q", p.Payload, payload)
	}
}

func TestParse_IPv6(t *testing.T) {
	src := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

	pkt := buildHEP3(
		chunk(chunkIPFamily, u8(10)),
		chunk(chunkIPv6Src, src),
		chunk(chunkProtocolType, u8(ProtocolTypeSIP)),
		chunk(chunkPayload, []byte("BYE sip:x SIP/2.0\r\n\r\n")),
	)

	p, err := Parse(pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got := p.SrcIP.String(); got != "2001:db8::1" {
		t.Errorf("SrcIP = %q, want 2001:db8::1", got)
	}
}

func TestParse_NonSIPType(t *testing.T) {
	pkt := buildHEP3(
		chunk(chunkProtocolType, u8(5)), // RTCP, not SIP
		chunk(chunkPayload, []byte("not sip")),
	)

	p, err := Parse(pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if p.IsSIP() {
		t.Error("IsSIP = true for RTCP payload, want false")
	}
}

func TestParse_NoTimestampLeavesZero(t *testing.T) {
	pkt := buildHEP3(
		chunk(chunkProtocolType, u8(ProtocolTypeSIP)),
		chunk(chunkPayload, []byte("INVITE x SIP/2.0\r\n\r\n")),
	)

	p, err := Parse(pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !p.Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want zero (no chunk → ingest fills now)", p.Timestamp)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want error
	}{
		{"too short", []byte{'H', 'E', 'P'}, ErrTooShort},
		{"bad magic", buildBadMagic(), ErrBadMagic},
		{"length exceeds buffer", []byte{'H', 'E', 'P', '3', 0xFF, 0xFF}, ErrTooShort},
		{"malformed chunk length", buildBadChunk(), ErrBadChunk},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.buf)
			if err != tt.want {
				t.Errorf("Parse err = %v, want %v", err, tt.want)
			}
		})
	}
}

func buildBadMagic() []byte {
	b := buildHEP3(chunk(chunkProtocolType, u8(1)))
	b[0] = 'X'

	return b
}

// buildBadChunk declares a chunk length that runs past the packet end.
func buildBadChunk() []byte {
	// "HEP3" + total len 12, then a chunk claiming length 100.
	b := make([]byte, 12)
	copy(b[0:4], "HEP3")
	binary.BigEndian.PutUint16(b[4:6], 12)
	binary.BigEndian.PutUint16(b[6:8], 0)   // vendor
	binary.BigEndian.PutUint16(b[8:10], 11) // type
	binary.BigEndian.PutUint16(b[10:12], 100)

	return b
}

// TestParse_SkipsUnknownChunks proves the length-walk steps over chunks
// we don't decode without losing sync on the ones we do.
func TestParse_SkipsUnknownChunks(t *testing.T) {
	pkt := buildHEP3(
		chunk(0x00FF, []byte("vendor junk we ignore")), // unknown type
		chunk(chunkProtocolType, u8(ProtocolTypeSIP)),
		chunk(0x00FE, []byte("more junk")),
		chunk(chunkPayload, []byte("INVITE x SIP/2.0\r\n\r\n")),
	)

	p, err := Parse(pkt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !p.IsSIP() || string(p.Payload) != "INVITE x SIP/2.0\r\n\r\n" {
		t.Errorf("unknown chunks broke parsing: IsSIP=%v payload=%q", p.IsSIP(), p.Payload)
	}
}
