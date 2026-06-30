package ingest

import (
	"net"
	"testing"
	"time"

	"github.com/thadeu/clowk-hep3/internal/hep"
	"github.com/thadeu/clowk-hep3/internal/models"
)

// fakeSink collects enqueued messages for assertions.
type fakeSink struct {
	msgs []models.SipMessage
}

func (f *fakeSink) Enqueue(m models.SipMessage) { f.msgs = append(f.msgs, m) }

func sipPacket(payload string) *hep.Packet {
	return &hep.Packet{
		ProtocolType:   hep.ProtocolTypeSIP,
		SrcIP:          net.IPv4(1, 2, 3, 4),
		DstIP:          net.IPv4(5, 6, 7, 8),
		SrcPort:        5060,
		DstPort:        5061,
		CaptureAgentID: 100,
		Payload:        []byte(payload),
	}
}

func newProc(sink Sink, discard map[string]struct{}, now func() time.Time) *Processor {
	return NewProcessor(Options{
		Sink:              sink,
		DedupWindow:       500 * time.Millisecond,
		CorrelationHeader: "X-CID",
		DiscardMethods:    discard,
		Now:               now,
	})
}

func TestProcess_StoresSIP(t *testing.T) {
	sink := &fakeSink{}
	fixed := time.Unix(1700000000, 0).UTC()
	p := newProc(sink, nil, func() time.Time { return fixed })

	pkt := sipPacket("INVITE sip:bob@x SIP/2.0\r\nFrom: <sip:alice@x>;tag=1\r\nTo: <sip:bob@x>\r\nCall-ID: c1\r\nX-CID: corr1\r\n\r\n")

	if r := p.Process(pkt); r != Stored {
		t.Fatalf("Process = %v, want Stored", r)
	}

	if len(sink.msgs) != 1 {
		t.Fatalf("enqueued %d, want 1", len(sink.msgs))
	}

	m := sink.msgs[0]

	if m.CallID != "c1" || m.Method != "INVITE" || m.XCID != "corr1" {
		t.Errorf("bad message: callid=%q method=%q xcid=%q", m.CallID, m.Method, m.XCID)
	}

	if m.FromUser != "alice" || m.ToUser != "bob" {
		t.Errorf("users: from=%q to=%q", m.FromUser, m.ToUser)
	}

	if m.SrcIP != "1.2.3.4" || m.DstIP != "5.6.7.8" {
		t.Errorf("ips: src=%q dst=%q", m.SrcIP, m.DstIP)
	}

	if m.NodeID != 100 {
		t.Errorf("NodeID = %d, want 100", m.NodeID)
	}
}

func TestProcess_DropsNonSIP(t *testing.T) {
	sink := &fakeSink{}
	p := newProc(sink, nil, nil)

	pkt := &hep.Packet{ProtocolType: 5, Payload: []byte("rtcp")} // not SIP

	if r := p.Process(pkt); r != DroppedNonSIP {
		t.Errorf("Process = %v, want DroppedNonSIP", r)
	}

	if len(sink.msgs) != 0 {
		t.Errorf("enqueued %d, want 0", len(sink.msgs))
	}
}

func TestProcess_DropsDuplicate(t *testing.T) {
	sink := &fakeSink{}
	fixed := time.Unix(1700000000, 0)
	p := newProc(sink, nil, func() time.Time { return fixed })

	payload := "INVITE sip:bob@x SIP/2.0\r\nCall-ID: dup\r\n\r\n"

	if r := p.Process(sipPacket(payload)); r != Stored {
		t.Fatalf("first = %v, want Stored", r)
	}

	if r := p.Process(sipPacket(payload)); r != DroppedDuplicate {
		t.Errorf("second = %v, want DroppedDuplicate", r)
	}

	if len(sink.msgs) != 1 {
		t.Errorf("enqueued %d, want 1 (dup must not store)", len(sink.msgs))
	}
}

func TestProcess_DropsDiscardedMethod(t *testing.T) {
	sink := &fakeSink{}
	discard := map[string]struct{}{"OPTIONS": {}}
	p := newProc(sink, discard, nil)

	pkt := sipPacket("OPTIONS sip:x SIP/2.0\r\nCall-ID: o1\r\n\r\n")

	if r := p.Process(pkt); r != DroppedMethod {
		t.Errorf("Process = %v, want DroppedMethod", r)
	}

	if len(sink.msgs) != 0 {
		t.Error("OPTIONS was stored despite discard set")
	}
}

// A response (200 OK) to a discarded method must be dropped too — it has
// no request method, so the filter keys off the CSeq method.
func TestProcess_DropsDiscardedResponseByCSeq(t *testing.T) {
	sink := &fakeSink{}
	discard := map[string]struct{}{"OPTIONS": {}}
	p := newProc(sink, discard, nil)

	pkt := sipPacket("SIP/2.0 200 OK\r\nCall-ID: o2\r\nCSeq: 10 OPTIONS\r\n\r\n")

	if r := p.Process(pkt); r != DroppedMethod {
		t.Errorf("Process = %v, want DroppedMethod (OPTIONS response)", r)
	}

	if len(sink.msgs) != 0 {
		t.Error("OPTIONS 200 OK was stored despite discard set")
	}
}

// A response to a NON-discarded method (200 OK to INVITE) is kept.
func TestProcess_KeepsNonDiscardedResponse(t *testing.T) {
	sink := &fakeSink{}
	discard := map[string]struct{}{"OPTIONS": {}}
	p := newProc(sink, discard, nil)

	pkt := sipPacket("SIP/2.0 200 OK\r\nCall-ID: c1\r\nCSeq: 1 INVITE\r\n\r\n")

	if r := p.Process(pkt); r != Stored {
		t.Errorf("Process = %v, want Stored (INVITE 200 OK)", r)
	}
}

func TestProcess_DropsNoCallID(t *testing.T) {
	sink := &fakeSink{}
	p := newProc(sink, nil, nil)

	pkt := sipPacket("INVITE sip:x SIP/2.0\r\n\r\n") // no Call-ID

	if r := p.Process(pkt); r != DroppedNoCallID {
		t.Errorf("Process = %v, want DroppedNoCallID", r)
	}
}

// SIP header correlation must win over the HEP correlation chunk.
func TestProcess_HeaderCorrelationWinsOverChunk(t *testing.T) {
	sink := &fakeSink{}
	p := newProc(sink, nil, nil)

	pkt := sipPacket("INVITE sip:x SIP/2.0\r\nCall-ID: c\r\nX-CID: from-header\r\n\r\n")
	pkt.CorrelationID = "from-chunk"

	p.Process(pkt)

	if sink.msgs[0].XCID != "from-header" {
		t.Errorf("XCID = %q, want from-header (SIP header beats HEP chunk)", sink.msgs[0].XCID)
	}
}

// When the SIP header is absent, fall back to the HEP correlation chunk.
func TestProcess_FallsBackToChunkCorrelation(t *testing.T) {
	sink := &fakeSink{}
	p := newProc(sink, nil, nil)

	pkt := sipPacket("INVITE sip:x SIP/2.0\r\nCall-ID: c\r\n\r\n")
	pkt.CorrelationID = "from-chunk"

	p.Process(pkt)

	if sink.msgs[0].XCID != "from-chunk" {
		t.Errorf("XCID = %q, want from-chunk fallback", sink.msgs[0].XCID)
	}
}

// A packet with no timestamp chunk must get the receive time, not zero.
func TestProcess_TimestampFallback(t *testing.T) {
	sink := &fakeSink{}
	fixed := time.Unix(1700000123, 0).UTC()
	p := newProc(sink, nil, func() time.Time { return fixed })

	pkt := sipPacket("INVITE sip:x SIP/2.0\r\nCall-ID: c\r\n\r\n") // Timestamp zero

	p.Process(pkt)

	if !sink.msgs[0].TS.Equal(fixed) {
		t.Errorf("TS = %v, want fallback %v", sink.msgs[0].TS, fixed)
	}
}

// A packet that carries its own timestamp keeps it (no fallback).
func TestProcess_TimestampFromPacket(t *testing.T) {
	sink := &fakeSink{}
	now := time.Unix(1700009999, 0)
	p := newProc(sink, nil, func() time.Time { return now })

	pkt := sipPacket("INVITE sip:x SIP/2.0\r\nCall-ID: c\r\n\r\n")
	pktTS := time.Unix(1700000000, 0).UTC()
	pkt.Timestamp = pktTS

	p.Process(pkt)

	if !sink.msgs[0].TS.Equal(pktTS) {
		t.Errorf("TS = %v, want packet ts %v (not now)", sink.msgs[0].TS, pktTS)
	}
}
