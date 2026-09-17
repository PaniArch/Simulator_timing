package memsys

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// The port buffer is VX_mem_unit.g_flush_port[0].dcache_flush_arb's
// b-dflush boundary. Both real words (including inline flush) and synthetic
// host flushes use the same two registered slots. Port 1 is a wire.
// FlushOffer remains the software scan + Visibility operation of Cache;
// it is not a fabricated load or an extra ISA response.
type dcachePortRequest struct {
	word  WordOffer
	flush FlushOffer
}

type dcachePortBuffer struct {
	queue       cacheQueue[dcachePortRequest]
	activeFlush FlushOffer // ownership lasts through delivery, not queue pop
	heldFlush   FlushOffer
	lastFlush   uint64
	usedFlush   bool
}

func newDCachePortBuffer() (*dcachePortBuffer, error) {
	b, err := timing.Buffer("b-dflush")
	if err != nil {
		return nil, err
	}
	if b.Size != 2 || b.OutReg != 1 {
		return nil, fmt.Errorf("unsupported b-dflush buffer: %+v", b)
	}
	return &dcachePortBuffer{queue: cacheQueue[dcachePortRequest]{spec: b}}, nil
}

// output is an old-edge view: a newly accepted word cannot reach Cache until
// the next edge. In particular, a full buffer cannot reuse departing credit.
func (b *dcachePortBuffer) output(core []WordOffer) ([]WordOffer, FlushOffer) {
	requests := []WordOffer{{}, core[1]}
	var flush FlushOffer
	if len(b.queue.values) != 0 {
		requests[0], flush = b.queue.values[0].word, b.queue.values[0].flush
	}
	return requests, flush
}

func (b *dcachePortBuffer) validate(flush FlushOffer) error {
	if b.heldFlush.Valid && b.heldFlush != flush {
		return fmt.Errorf("changed stalled D-cache port flush")
	}
	if flush.Valid && b.usedFlush && flush.Identity.Transaction <= b.lastFlush {
		return fmt.Errorf("stale D-cache port flush transaction")
	}
	return nil
}

// advance returns upstream handshakes separately from Cache's downstream
// handshakes. The adapter retains its word records after these acceptances,
// so neither a buffered store nor a cancelled load loses its identity tail.
func (b *dcachePortBuffer) advance(core []WordOffer, flush FlushOffer, cache CacheEdge) ([]bool, bool) {
	accepted := []bool{false, cache.Accepted[1]}
	flushAccepted := false
	var push *dcachePortRequest
	if b.queue.ready(false) {
		// VX_dcr_flush uses sticky priority with core on input 0. The
		// synthetic input drops after one acceptance, so a previous flush
		// grant cannot retain priority over a subsequent real request.
		if core[0].Valid {
			push = &dcachePortRequest{word: core[0]}
			accepted[0] = true
		} else if flush.Valid && !b.activeFlush.Valid {
			push = &dcachePortRequest{flush: flush}
			flushAccepted = true
		}
	}
	b.queue.update(cache.Accepted[0] || cache.FlushAccepted, push)
	if cache.Flush.Delivered {
		b.activeFlush = FlushOffer{}
	}
	if flushAccepted {
		b.activeFlush = flush
		b.lastFlush, b.usedFlush = flush.Identity.Transaction, true
	}
	b.heldFlush = FlushOffer{}
	if flush.Valid && !flushAccepted {
		b.heldFlush = flush
	}
	return accepted, flushAccepted
}

func (b *dcachePortBuffer) Drained() bool {
	return len(b.queue.values) == 0 && !b.activeFlush.Valid && !b.heldFlush.Valid
}

func (b *dcachePortBuffer) HasResidency(kernel, cta uint64) bool {
	belongs := func(id Identity) bool { return id.Kernel == kernel && id.CTA == cta }
	for _, r := range b.queue.values {
		if r.word.Valid && belongs(r.word.Request.Identity) || r.flush.Valid && belongs(r.flush.Identity) {
			return true
		}
	}
	return b.activeFlush.Valid && belongs(b.activeFlush.Identity) || b.heldFlush.Valid && belongs(b.heldFlush.Identity)
}
