package ingest

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/thadeu/clowk-hep3/internal/hep"
)

// maxDatagram caps a single HEP3 read. SIP messages are well under this;
// it is sized to the practical UDP datagram ceiling.
const maxDatagram = 65535

// Receiver accepts HEP3 over UDP (and optionally TCP), decodes each
// datagram, and fans the decoded packets out to a worker pool that runs
// them through the Processor. Decoding happens in the read loop (cheap,
// and it copies the payload out of the read buffer so the buffer can be
// reused); the heavier SIP parse + dedup + enqueue runs in workers.
type Receiver struct {
	proc    *Processor
	workers int
	log     *log.Logger
	ch      chan *hep.Packet
}

// NewReceiver builds a Receiver. workers < 1 is clamped to 1.
func NewReceiver(proc *Processor, workers int, logger *log.Logger) *Receiver {
	if workers < 1 {
		workers = 1
	}

	if logger == nil {
		logger = log.Default()
	}

	return &Receiver{
		proc:    proc,
		workers: workers,
		log:     logger,
		ch:      make(chan *hep.Packet, workers*64),
	}
}

// Run starts the worker pool and the configured listeners, blocking
// until ctx is cancelled. udpAddr is required; tcpAddr is optional
// (empty string disables the TCP listener).
func (r *Receiver) Run(ctx context.Context, udpAddr, tcpAddr string) error {
	var wg sync.WaitGroup

	wg.Add(r.workers)

	for range r.workers {
		go func() {
			defer wg.Done()

			for pkt := range r.ch {
				r.proc.Process(pkt)
			}
		}()
	}

	errCh := make(chan error, 2)

	var listeners sync.WaitGroup

	listeners.Add(1)

	go func() {
		defer listeners.Done()

		if err := r.serveUDP(ctx, udpAddr); err != nil {
			errCh <- err
		}
	}()

	if tcpAddr != "" {
		listeners.Add(1)

		go func() {
			defer listeners.Done()

			if err := r.serveTCP(ctx, tcpAddr); err != nil {
				errCh <- err
			}
		}()
	}

	// Wait for either a listener to fail or ctx to cancel.
	go func() {
		listeners.Wait()
		close(errCh)
	}()

	var runErr error

	select {
	case <-ctx.Done():
	case err, ok := <-errCh:
		if ok && err != nil {
			runErr = err
		}
	}

	// Drain: stop accepting, let workers finish what's queued.
	listeners.Wait()
	close(r.ch)
	wg.Wait()

	return runErr
}

// serveUDP reads HEP3 datagrams until ctx is cancelled.
func (r *Receiver) serveUDP(ctx context.Context, addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}

	r.log.Printf("hep3: listening for HEP/UDP on %s", addr)

	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, maxDatagram)

	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			// Transient read error — log sparsely and keep going.
			r.log.Printf("hep3: udp read: %v", err)

			continue
		}

		pkt, perr := hep.Parse(buf[:n])
		if perr != nil {
			continue
		}

		r.dispatch(ctx, pkt)
	}
}

// serveTCP accepts HEP3-over-TCP connections. Each connection is a
// stream of length-framed HEP3 packets (the 2-byte total-length header
// frames each one).
func (r *Receiver) serveTCP(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	r.log.Printf("hep3: listening for HEP/TCP on %s", addr)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			r.log.Printf("hep3: tcp accept: %v", err)

			continue
		}

		go r.handleTCPConn(ctx, conn)
	}
}

func (r *Receiver) handleTCPConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	header := make([]byte, 6)

	for {
		if ctx.Err() != nil {
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}

		if header[0] != 'H' || header[1] != 'E' || header[2] != 'P' || header[3] != '3' {
			// Out of frame — we can't resync a corrupt stream safely.
			return
		}

		total := int(binary.BigEndian.Uint16(header[4:6]))
		if total < 6 || total > maxDatagram {
			return
		}

		full := make([]byte, total)
		copy(full, header)

		if _, err := io.ReadFull(conn, full[6:]); err != nil {
			return
		}

		pkt, perr := hep.Parse(full)
		if perr != nil {
			continue
		}

		r.dispatch(ctx, pkt)
	}
}

// dispatch hands a decoded packet to the worker pool, dropping it if the
// queue is full (back-pressure: better to lose a capture line than to
// stall the read loop and drop datagrams at the kernel socket buffer,
// which would be silent). Returns once queued or dropped.
func (r *Receiver) dispatch(ctx context.Context, pkt *hep.Packet) {
	select {
	case r.ch <- pkt:
	case <-ctx.Done():
	default:
		// Queue full — drop. A counter could surface this; for the MVP
		// we keep the read loop non-blocking.
	}
}
