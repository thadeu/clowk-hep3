// Package hep decodes HEP3 (Homer Encapsulation Protocol v3) datagrams.
//
// HEP3 is a thin binary envelope a SIP proxy (Kamailio, FreeSWITCH, …)
// sends as a side-channel copy of every signaling message. The wire
// format, confirmed against the canonical sipcapture/hep-go reader:
//
//	bytes 0..4   ASCII "HEP3"
//	bytes 4..6   total packet length, uint16 big-endian (covers the
//	             whole datagram, including these 6 header bytes)
//	bytes 6..    a sequence of generic chunks (TLV)
//
// Each chunk is:
//
//	2 bytes  vendor id   (big-endian; 0x0000 == generic)
//	2 bytes  type id     (big-endian)
//	2 bytes  length      (big-endian; INCLUDES the 6-byte chunk header)
//	N bytes  value       (length-6 bytes)
//
// We only decode the generic (vendor 0x0000) chunks we persist and skip
// everything else by walking the length field — the spec guarantees an
// unknown chunk can be stepped over without understanding it.
package hep

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"time"
)

// ProtocolTypeSIP is the value of the protocol-type chunk (0x000b) for
// SIP signaling. We act only on SIP; every other payload type is parsed
// but ignored by the ingest pipeline.
const ProtocolTypeSIP = 1

// Generic chunk type ids (vendor 0x0000).
const (
	chunkIPFamily      = 0x0001 // 1 byte: 2=AF_INET, 10=AF_INET6
	chunkIPProtocol    = 0x0002 // 1 byte: 6=TCP, 17=UDP
	chunkIPv4Src       = 0x0003 // 4 bytes
	chunkIPv4Dst       = 0x0004 // 4 bytes
	chunkIPv6Src       = 0x0005 // 16 bytes
	chunkIPv6Dst       = 0x0006 // 16 bytes
	chunkSrcPort       = 0x0007 // 2 bytes
	chunkDstPort       = 0x0008 // 2 bytes
	chunkTimeSec       = 0x0009 // 4 bytes (unix seconds)
	chunkTimeMicro     = 0x000a // 4 bytes (microseconds)
	chunkProtocolType  = 0x000b // 1 byte (see ProtocolTypeSIP)
	chunkCaptureAgent  = 0x000c // 4 bytes (node id)
	chunkPayload       = 0x000f // raw payload (the SIP message)
	chunkCorrelationID = 0x0011 // string (HEP-level correlation id)
)

// Parse errors. Callers treat all of them as "drop this datagram".
var (
	ErrTooShort = errors.New("hep: datagram too short")
	ErrBadMagic = errors.New("hep: not a HEP3 datagram")
	ErrBadChunk = errors.New("hep: malformed chunk")
)

var magic = []byte{'H', 'E', 'P', '3'}

// Packet is a decoded HEP3 envelope, reduced to the fields clowk-hep3
// persists. Timestamp is zero when the datagram carried no timestamp
// chunk — the ingest layer substitutes the receive time in that case,
// keeping this parser free of any clock dependency (so tests are
// deterministic).
type Packet struct {
	IPFamily       uint8
	IPProtocol     uint8
	SrcIP          net.IP
	DstIP          net.IP
	SrcPort        uint16
	DstPort        uint16
	Timestamp      time.Time
	ProtocolType   uint8
	CaptureAgentID uint32
	CorrelationID  string
	Payload        []byte
}

// IsSIP reports whether the datagram carried a SIP payload.
func (p *Packet) IsSIP() bool {
	return p.ProtocolType == ProtocolTypeSIP
}

// Parse decodes one HEP3 datagram.
//
// The returned Packet's byte fields (Payload, SrcIP, DstIP) are copied
// out of buf, so the caller is free to reuse buf for the next read once
// Parse returns. Copying the payload is the price of a reusable read
// buffer; at SIP volumes it is negligible.
func Parse(buf []byte) (*Packet, error) {
	if len(buf) < 6 {
		return nil, ErrTooShort
	}

	if !bytes.Equal(buf[0:4], magic) {
		return nil, ErrBadMagic
	}

	total := int(binary.BigEndian.Uint16(buf[4:6]))
	if total < 6 || total > len(buf) {
		return nil, ErrTooShort
	}

	p := &Packet{}

	var sec, usec uint32

	off := 6
	for off+6 <= total {
		typ := binary.BigEndian.Uint16(buf[off+2 : off+4])
		length := int(binary.BigEndian.Uint16(buf[off+4 : off+6]))

		// A chunk must at least span its own header and stay inside
		// the declared packet length. Anything else is a corrupt
		// stream — bail rather than guess.
		if length < 6 || off+length > total {
			return nil, ErrBadChunk
		}

		body := buf[off+6 : off+length]

		switch typ {
		case chunkIPFamily:
			if len(body) >= 1 {
				p.IPFamily = body[0]
			}

		case chunkIPProtocol:
			if len(body) >= 1 {
				p.IPProtocol = body[0]
			}

		case chunkIPv4Src:
			if len(body) >= 4 {
				p.SrcIP = cloneIP(body[:4])
			}

		case chunkIPv4Dst:
			if len(body) >= 4 {
				p.DstIP = cloneIP(body[:4])
			}

		case chunkIPv6Src:
			if len(body) >= 16 {
				p.SrcIP = cloneIP(body[:16])
			}

		case chunkIPv6Dst:
			if len(body) >= 16 {
				p.DstIP = cloneIP(body[:16])
			}

		case chunkSrcPort:
			if len(body) >= 2 {
				p.SrcPort = binary.BigEndian.Uint16(body)
			}

		case chunkDstPort:
			if len(body) >= 2 {
				p.DstPort = binary.BigEndian.Uint16(body)
			}

		case chunkTimeSec:
			if len(body) >= 4 {
				sec = binary.BigEndian.Uint32(body)
			}

		case chunkTimeMicro:
			if len(body) >= 4 {
				usec = binary.BigEndian.Uint32(body)
			}

		case chunkProtocolType:
			if len(body) >= 1 {
				p.ProtocolType = body[0]
			}

		case chunkCaptureAgent:
			if len(body) >= 4 {
				p.CaptureAgentID = binary.BigEndian.Uint32(body)
			}

		case chunkCorrelationID:
			p.CorrelationID = string(body)

		case chunkPayload:
			p.Payload = append([]byte(nil), body...)
		}

		off += length
	}

	if sec > 0 {
		p.Timestamp = time.Unix(int64(sec), int64(usec)*1000).UTC()
	}

	return p, nil
}

func cloneIP(b []byte) net.IP {
	return net.IP(append([]byte(nil), b...))
}
