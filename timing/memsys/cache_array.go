package memsys

import "fmt"

// WordRequest is the cache-side transfer, after any SIMD coalescing. ByteEnable
// addresses bytes within one WordBytes word; Identity/Tag survive hit and miss.
// A store's application receipt is software bookkeeping, not an RTL load reply.
type WordRequest struct {
	Identity     Identity
	Tag          uint64
	Address      uint32
	Write        bool
	NonCacheable bool
	Flush        bool // flush scan precedes acceptance of this original word
	ByteEnable   uint8
	Data         [8]byte
}
type WordOffer struct {
	Valid   bool
	Request WordRequest
}
type WordResponse struct {
	Identity   Identity
	Tag        uint64
	Data       [8]byte
	ByteEnable uint8
	Err        error
}

type cacheLine struct {
	valid, dirty bool
	tag          uint32
	data         [SectorBytes]byte
}
type eviction struct {
	Valid   bool
	Address uint32
	Data    [SectorBytes]byte
}
type cacheArray struct {
	spec        CacheSpec
	bank        int
	lines       [][]cacheLine
	fifo        []int
	initialized []bool
}

func newCacheArray(s CacheSpec, bank int) *cacheArray {
	a := &cacheArray{spec: s, bank: bank, lines: make([][]cacheLine, s.Sets), fifo: make([]int, s.Sets), initialized: make([]bool, s.Sets)}
	for i := range a.lines {
		a.lines[i] = make([]cacheLine, s.Ways)
	}
	return a
}

// initSet models the reset scan, not a zero-cycle whole-array flush.
func (a *cacheArray) initSet(set int) {
	clear(a.lines[set])
	a.fifo[set] = 0
	a.initialized[set] = true
}
func (a *cacheArray) location(address uint32) (set, word int, tag uint32, err error) {
	bank, set, word, tag := a.spec.DecodeAddress(address)
	if bank != a.bank || !a.initialized[set] {
		return 0, 0, 0, fmt.Errorf("wrong bank or uninitialized cache set")
	}
	return set, word, tag, nil
}
func (a *cacheArray) lookup(address uint32) (int, bool, error) {
	set, _, tag, err := a.location(address)
	if err != nil {
		return 0, false, err
	}
	for way, line := range a.lines[set] {
		if line.valid && line.tag == tag {
			return way, true, nil
		}
	}
	return 0, false, nil
}
func (a *cacheArray) read(r WordRequest) (WordResponse, bool, error) {
	rsp := WordResponse{Identity: r.Identity, Tag: r.Tag, ByteEnable: uint8((1 << a.spec.WordBytes) - 1)}
	way, hit, err := a.lookup(r.Address)
	if err != nil || !hit {
		return rsp, hit, err
	}
	set, word, _, _ := a.location(r.Address)
	copy(rsp.Data[:a.spec.WordBytes], a.lines[set][way].data[word*a.spec.WordBytes:(word+1)*a.spec.WordBytes])
	return rsp, true, nil
}
func (a *cacheArray) write(r WordRequest) (bool, error) {
	if !a.spec.Writeback {
		return false, fmt.Errorf("store to read-only cache")
	}
	if r.ByteEnable>>a.spec.WordBytes != 0 {
		return false, fmt.Errorf("cache byte enable outside word")
	}
	way, hit, err := a.lookup(r.Address)
	if err != nil || !hit {
		return hit, err
	}
	set, word, _, _ := a.location(r.Address)
	line := &a.lines[set][way]
	for i := 0; i < a.spec.WordBytes; i++ {
		if r.ByteEnable&(1<<i) != 0 {
			line.data[word*a.spec.WordBytes+i] = r.Data[i]
		}
	}
	// The RTL tag dirty set is qualified by write/hit, not by a nonzero byteen.
	line.dirty = true
	return true, nil
}

// victim is a detached snapshot so a stalled writeback never points at storage
// subsequently replaced by a fill. DIRTY_BYTES=0 writes the complete sector.
func (a *cacheArray) victim(set, way int) eviction {
	line := a.lines[set][way]
	return eviction{Valid: line.valid && line.dirty, Address: a.spec.lineAddress(a.bank, set, line.tag), Data: line.data}
}
func (a *cacheArray) fill(address uint32, data [SectorBytes]byte) (eviction, error) {
	set, _, tag, err := a.location(address)
	if err != nil {
		return eviction{}, err
	}
	way := a.fifo[set]
	existing, hit, err := a.lookup(address)
	if err != nil {
		return eviction{}, err
	}
	if hit {
		way = existing
	}
	old := a.victim(set, way)
	if hit {
		old.Valid = false
	} // resident-sector refill does not evict that line
	a.lines[set][way] = cacheLine{valid: true, tag: tag, data: data}
	a.fifo[set] = (a.fifo[set] + 1) % a.spec.Ways
	return old, nil
}

// flushWay only changes the selected way. The caller must reserve a normal
// memory-request queue slot before calling when the returned victim is dirty.
func (a *cacheArray) flushWay(set, way int) eviction {
	old := a.victim(set, way)
	a.lines[set][way].valid = false
	a.lines[set][way].dirty = false
	return old
}
func (a *cacheArray) residentCounts() (valid, dirty int) {
	for _, set := range a.lines {
		for _, line := range set {
			if line.valid {
				valid++
			}
			if line.dirty {
				dirty++
			}
		}
	}
	return
}
