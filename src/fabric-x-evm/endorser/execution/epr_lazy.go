package execution

// Lazy program tracing. The program tracer costs more than the delta tracer, and most transactions of
// a delta-friendly contract never need a program. The endorser first executes with the delta tracer
// only; if some storage write is not expressible as a guarded delta, it reverts to the snapshot and
// re-executes with both tracers, and remembers the (contract, selector) so that later calls attach the
// program tracer directly. The chosen encoding (deltas if every write is one, else a program) is the
// same either way, so endorsers of different organizations still sign identical bytes.

import (
	"sync"

	"github.com/ethereum/go-ethereum/core"
)

var eprProgFns sync.Map // [24]byte (to address | selector) -> struct{}

func eprFnKey(msg *core.Message) ([24]byte, bool) {
	var k [24]byte
	if msg.To == nil || len(msg.Data) < 4 {
		return k, false
	}
	copy(k[:20], msg.To[:])
	copy(k[20:], msg.Data[:4])
	return k, true
}

// eprNeedsProg reports whether calls to this contract function have needed a program before.
func eprNeedsProg(msg *core.Message) bool {
	k, ok := eprFnKey(msg)
	if !ok {
		return true
	}
	_, hit := eprProgFns.Load(k)
	return hit
}

func eprNoteProg(msg *core.Message) {
	if k, ok := eprFnKey(msg); ok {
		eprProgFns.Store(k, struct{}{})
	}
}

// AllDeltas reports whether every storage write of the traced execution extracts as a guarded delta.
func (t *EPRTracer) AllDeltas() bool {
	if t.lost {
		return false
	}
	written := map[string]bool{}
	for s, st := range t.slots {
		if st.written {
			written[s] = true
		}
	}
	if t.gas.short {
		return false
	}
	for s := range written {
		if _, _, _, why := t.slots[s].extract(written, true); why != "" {
			return false
		}
	}
	return true
}
