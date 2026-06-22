// Package ingest receives HEP3 packets off the wire, deduplicates
// retransmits, parses the SIP payload, and hands SIP messages to a Sink for
// persistence.
package ingest

import (
	"net"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/thadeu/clowk-hep3/internal/hep"
	"github.com/thadeu/clowk-hep3/internal/models"
	"github.com/thadeu/clowk-hep3/internal/sip"
)

// Sink is the subset of *models.SipMessages the processor needs. An
// interface so tests substitute an in-memory collector.
type Sink interface {
	Enqueue(models.SipMessage)
}

// Processor turns a decoded HEP packet into a stored SIP message,
// applying dedup, method filtering, correlation, and timestamp
// fallback. It is the single funnel every datagram passes through.
type Processor struct {
	sink    Sink
	dedup   *Deduper
	corrHdr string
	discard map[string]struct{}
	now     func() time.Time
}

// Options configures a Processor.
type Options struct {
	Sink              Sink
	DedupWindow       time.Duration
	CorrelationHeader string
	DiscardMethods    map[string]struct{}
	// Now is injected for tests; nil defaults to time.Now.
	Now func() time.Time
}

// NewProcessor builds a Processor from Options.
func NewProcessor(o Options) *Processor {
	now := o.Now
	if now == nil {
		now = time.Now
	}

	return &Processor{
		sink:    o.Sink,
		dedup:   NewDeduper(o.DedupWindow),
		corrHdr: o.CorrelationHeader,
		discard: o.DiscardMethods,
		now:     now,
	}
}

// Result reports what the processor did with a packet — used by metrics
// and tests to distinguish the drop reasons.
type Result int

const (
	// Stored means the message was enqueued for persistence.
	Stored Result = iota
	// DroppedNonSIP means the packet was not a SIP payload.
	DroppedNonSIP
	// DroppedDuplicate means the payload was a within-window duplicate.
	DroppedDuplicate
	// DroppedMethod means the request method was in the discard set.
	DroppedMethod
	// DroppedNoCallID means the message had no Call-ID to group on.
	DroppedNoCallID
)

// Process runs one decoded packet through the funnel.
func (p *Processor) Process(pkt *hep.Packet) Result {
	if !pkt.IsSIP() || len(pkt.Payload) == 0 {
		return DroppedNonSIP
	}

	now := p.now()

	if p.dedup.Seen(xxhash.Sum64(pkt.Payload), now) {
		return DroppedDuplicate
	}

	msg := sip.Parse(string(pkt.Payload), p.corrHdr)

	if msg.IsRequest && len(p.discard) > 0 {
		if _, drop := p.discard[strings.ToUpper(msg.Method)]; drop {
			return DroppedMethod
		}
	}

	if msg.CallID == "" {
		return DroppedNoCallID
	}

	ts := pkt.Timestamp
	if ts.IsZero() {
		ts = now
	}

	// Correlation: the SIP header wins (operator-chosen, B2BUA-aware);
	// fall back to the HEP-level correlation chunk when the header is
	// absent.
	xcid := msg.Correlation
	if xcid == "" {
		xcid = pkt.CorrelationID
	}

	p.sink.Enqueue(models.SipMessage{
		TS:           ts,
		CallID:       msg.CallID,
		XCID:         xcid,
		Method:       msg.Method,
		ResponseCode: msg.ResponseCode,
		FromUser:     msg.FromUser,
		ToUser:       msg.ToUser,
		RURI:         msg.RURI,
		SrcIP:        ipString(pkt.SrcIP),
		DstIP:        ipString(pkt.DstIP),
		SrcPort:      int(pkt.SrcPort),
		DstPort:      int(pkt.DstPort),
		NodeID:       int(pkt.CaptureAgentID),
		UserAgent:    msg.UserAgent,
		CSeq:         msg.CSeq,
		RawSIP:       string(pkt.Payload),
	})

	return Stored
}

func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}

	return ip.String()
}
