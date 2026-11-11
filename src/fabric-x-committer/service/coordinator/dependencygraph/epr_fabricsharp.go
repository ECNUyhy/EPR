/*
Baseline (not upstream): FabricSharp's fine-grained concurrency control (Ruan et al., "A Transactional
Perspective on Execute-order-validate Blockchains", SIGMOD'20), in the Fabric-X coordinator. FX_FABRICSHARP=1
runs the authors' scheduler (epr_fabricsharp_orig.go). FX_FABRICSHARP=exact runs the idealized
reimplementation below (exact cycle detection, in-memory indices: an upper bound of FabricSharp), where every
block is processed in block order before dependency construction:

  Algorithm 2 (per transaction, in arrival order). Dependencies on committed and pending transactions are
  computed as in the paper's Section 4.3: predecessors are the last committed writer of each written key
  (ww), the writer of each version read (n-wr) and the committed and pending readers of each written key
  (rw); successors are the committed writers of a read key after the version read and its pending writers
  (anti-rw). c-ww among pending transactions is ignored. If a successor reaches a predecessor (a cycle),
  the schedule can never be serialized (Theorem 2) and the transaction is aborted early; otherwise it joins
  the graph.
  Algorithm 3 (per block). The accepted transactions are topologically sorted (ties: arrival order), the
  c-ww edges among them are restored in that order, and they are committed in that order.

As in the paper, committers do not validate read versions of accepted transactions (its Figure 8, "no
concurrency validation" at peers): an accepted transaction may have read a stale version, and is
serialized before the transaction that overwrote it. The validator-committer therefore receives accepted
transactions with their reads dropped (read-writes become blind writes); the transactions themselves, and
so their signatures, are unchanged. Early-aborted transactions are reported with ABORTED_MVCC_CONFLICT.

Cycle detection is exact (graph search) instead of the paper's Bloom filters, which only add false-positive
aborts. Committed transactions older than fsWindowBlocks (max_span) blocks leave the graph, like the paper's
pruning and relay filters; a read of a version older than that window is aborted, as Fabric would.
*/

package dependencygraph

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
	fscommon "github.com/hyperledger/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp/common"
)

// FabricSharpOn enables the FabricSharp baseline: FX_FABRICSHARP=1 runs the authors' scheduler
// (epr_fabricsharp_orig.go), FX_FABRICSHARP=exact the idealized reimplementation in this file.
var FabricSharpOn = os.Getenv("FX_FABRICSHARP") == "1" || fsExact

var fsExact = os.Getenv("FX_FABRICSHARP") == "exact"

// fsWindowBlocks is the paper's max_span (Section 4.6; fixed to 10 in its experiments, Section 5.2):
// committed transactions older than that many blocks leave the graph, and a read whose overwriting write
// left the window is aborted. FX_FS_SPAN overrides it (sensitivity runs only).
var fsWindowBlocks = fsSpan()

func fsSpan() uint64 {
	var n uint64
	if _, err := fmt.Sscanf(os.Getenv("FX_FS_SPAN"), "%d", &n); err != nil || n == 0 {
		return 10
	}
	return n
}

var (
	fsAcceptedCnt, fsAbortedCnt, fsStaleAcceptedCnt, fsBlocks atomic.Int64
	fsStripped                                                sync.Map // tx ID -> VC namespaces without reads
	fsState                                                   = newFabricSharp()
)

func init() {
	if !FabricSharpOn {
		return
	}
	go func() {
		for range time.Tick(5 * time.Second) {
			logger.Infof("PILOT_FABRICSHARP blocks=%d accepted=%d stale_accepted=%d early_aborted=%d "+
				"span_dropped=%d deserted=%d no_snapshot=%d exact=%v",
				fsBlocks.Load(), fsAcceptedCnt.Load(), fsStaleAcceptedCnt.Load(), fsAbortedCnt.Load(),
				fsSpanDroppedCnt.Load(), fscommon.Warnings.Load(), fsNoSnapCnt.Load(), fsExact)
		}
	}()
}

type fsNode struct {
	succ   map[*fsNode]struct{}
	block  uint64
	pruned bool
}

type fsWriter struct {
	ver int64
	n   *fsNode
}

type fsKey struct {
	known   bool
	cur     int64 // version of the latest committed write
	writers []fsWriter
	readers []*fsNode
}

type fabricSharp struct {
	keys  map[string]*fsKey
	nodes []*fsNode // committed, in commit order
}

func newFabricSharp() *fabricSharp { return &fabricSharp{keys: map[string]*fsKey{}} }

type fsAccess struct {
	reads  map[string]int64
	writes map[string]bool
}

func fsVer(v *uint64) int64 {
	if v == nil {
		return -1 // the key did not exist
	}
	return int64(*v) //nolint:gosec
}

func fsCompositeKey(ns string, k []byte) string { return ns + "\x00" + string(k) }

// fsAccesses returns the keys a transaction reads (with the versions read) and writes; ok is false for
// transactions touching a system namespace, which are left alone.
func fsAccesses(tx *servicepb.TxWithRef) (fsAccess, bool) {
	a := fsAccess{reads: map[string]int64{}, writes: map[string]bool{}}
	if tx.Content == nil {
		return a, false
	}
	for _, ns := range tx.Content.Namespaces {
		if ns.NsId == committerpb.MetaNamespaceID || ns.NsId == committerpb.ConfigNamespaceID {
			return a, false
		}
		for _, r := range ns.ReadsOnly {
			a.reads[fsCompositeKey(ns.NsId, r.Key)] = fsVer(r.Version)
		}
		for _, rw := range ns.ReadWrites {
			k := fsCompositeKey(ns.NsId, rw.Key)
			a.reads[k] = fsVer(rw.Version)
			a.writes[k] = true
		}
		for _, w := range ns.BlindWrites {
			a.writes[fsCompositeKey(ns.NsId, w.Key)] = true
		}
	}
	return a, true
}

// key returns the state of a key, pruning entries that left the window.
func (f *fabricSharp) key(k string, cutoff uint64) *fsKey {
	ks := f.keys[k]
	if ks == nil {
		ks = &fsKey{}
		f.keys[k] = ks
	}
	if len(ks.writers) > 1 && ks.writers[0].n.block < cutoff {
		i := sort.Search(len(ks.writers)-1, func(i int) bool { return ks.writers[i].n.block >= cutoff })
		ks.writers = append([]fsWriter(nil), ks.writers[i:]...) // keep at least the last writer
	}
	if len(ks.readers) > 0 && ks.readers[0].block < cutoff {
		i := sort.Search(len(ks.readers), func(i int) bool { return ks.readers[i].block >= cutoff })
		ks.readers = append([]*fsNode(nil), ks.readers[i:]...)
	}
	return ks
}

// reaches reports whether any node in from reaches any node in to.
func fsReaches(from, to map[*fsNode]struct{}) bool {
	if len(from) == 0 || len(to) == 0 {
		return false
	}
	seen := map[*fsNode]bool{}
	stack := make([]*fsNode, 0, len(from))
	for n := range from {
		stack = append(stack, n)
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] || n.pruned {
			continue
		}
		seen[n] = true
		if _, ok := to[n]; ok {
			return true
		}
		for s := range n.succ {
			if !seen[s] {
				stack = append(stack, s)
			}
		}
	}
	return false
}

// FabricSharpBlock reorders a block's transactions and moves the early-aborted ones to its rejected list.
// It must be called for blocks in block order, from one goroutine.
func FabricSharpBlock(blk *servicepb.CoordinatorBatch) {
	if len(blk.Txs) == 0 {
		return
	}
	fsBlocks.Add(1)
	if !fsExact {
		fabricSharpOrigBlock(blk)
		return
	}
	f := fsState
	blockNum := blk.Txs[0].Ref.BlockNum
	var cutoff uint64
	if blockNum > fsWindowBlocks {
		cutoff = blockNum - fsWindowBlocks
	}

	var (
		passthrough []*servicepb.TxWithRef
		keep        []*servicepb.TxWithRef
		pend        []*fsNode
		acc         []fsAccess
		staleRead   []bool
		pw          = map[string][]*fsNode{}
		pr          = map[string][]*fsNode{}
	)
	for _, tx := range blk.Txs {
		a, ok := fsAccesses(tx)
		if !ok {
			passthrough = append(passthrough, tx)
			continue
		}
		n := &fsNode{succ: map[*fsNode]struct{}{}, block: blockNum}
		preds, succs := map[*fsNode]struct{}{}, map[*fsNode]struct{}{}
		abort, stale := false, false
		for k, v := range a.reads {
			ks := f.key(k, cutoff)
			if !ks.known || v > ks.cur { // first sight, or a base version we could not see (e.g. genesis)
				ks.known, ks.cur = true, v
			}
			if v < ks.cur {
				stale = true
				covered := false
				for _, w := range ks.writers {
					if w.ver > v {
						succs[w.n] = struct{}{} // anti-rw on a committed writer
						if w.ver == v+1 {
							covered = true
						}
					}
				}
				if !covered { // the overwriting write left the window
					abort = true
				}
			}
			for _, w := range ks.writers {
				if w.ver == v {
					preds[w.n] = struct{}{} // n-wr
				}
			}
			for _, p := range pw[k] {
				succs[p] = struct{}{} // anti-rw on a pending writer
			}
		}
		for k := range a.writes {
			ks := f.key(k, cutoff)
			if l := len(ks.writers); l > 0 {
				preds[ks.writers[l-1].n] = struct{}{} // ww
			}
			for _, r := range ks.readers {
				preds[r] = struct{}{} // rw (committed)
			}
			for _, r := range pr[k] {
				preds[r] = struct{}{} // rw (pending)
			}
		}
		if !abort {
			for s := range succs {
				if _, ok := preds[s]; ok {
					abort = true
					break
				}
			}
		}
		if abort || fsReaches(succs, preds) {
			fsAbortedCnt.Add(1)
			blk.Rejected = append(blk.Rejected, &committerpb.TxStatus{
				Ref: tx.Ref, Status: committerpb.Status_ABORTED_MVCC_CONFLICT,
			})
			continue
		}
		for p := range preds {
			if !p.pruned { // a pruned node left the window (the last writer of a key is kept for ww): no edge
				p.succ[n] = struct{}{}
			}
		}
		for s := range succs {
			n.succ[s] = struct{}{}
		}
		i := len(pend)
		pend, acc, keep, staleRead = append(pend, n), append(acc, a), append(keep, tx), append(staleRead, stale)
		for k := range a.reads {
			pr[k] = append(pr[k], pend[i])
		}
		for k := range a.writes {
			pw[k] = append(pw[k], pend[i])
		}
	}

	// Algorithm 3: topological order of the accepted transactions (ties: arrival order).
	idx := make(map[*fsNode]int, len(pend))
	for i, n := range pend {
		idx[n] = i
	}
	indeg := make([]int, len(pend))
	for _, n := range pend {
		for s := range n.succ {
			if j, ok := idx[s]; ok {
				indeg[j]++
			}
		}
	}
	order := make([]int, 0, len(pend))
	ready := []int{}
	for i := range pend {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	for len(ready) > 0 {
		sort.Ints(ready)
		i := ready[0]
		ready = ready[1:]
		order = append(order, i)
		for s := range pend[i].succ {
			if j, ok := idx[s]; ok {
				if indeg[j]--; indeg[j] == 0 {
					ready = append(ready, j)
				}
			}
		}
	}
	if len(order) != len(pend) { // cannot happen: Algorithm 2 keeps the graph acyclic
		panic("FabricSharp: cyclic pending schedule")
	}

	// restore c-ww in commit order and commit
	lastW := map[string]*fsNode{}
	out := make([]*servicepb.TxWithRef, 0, len(passthrough)+len(order))
	out = append(out, passthrough...)
	for _, i := range order {
		n, a := pend[i], acc[i]
		for k := range a.writes {
			if p := lastW[k]; p != nil {
				p.succ[n] = struct{}{}
			}
			lastW[k] = n
		}
		for k := range a.reads {
			ks := f.keys[k]
			ks.readers = append(ks.readers, n)
		}
		for k := range a.writes {
			ks := f.keys[k]
			if !ks.known {
				ks.known, ks.cur = true, -1 // a blind write to a key never seen: created at version 0
			}
			ks.cur++
			ks.writers = append(ks.writers, fsWriter{ver: ks.cur, n: n})
		}
		f.nodes = append(f.nodes, n)
		tx := keep[i]
		fsStripped.Store(tx.Ref.TxId, fsWithoutReads(tx.Content.Namespaces))
		out = append(out, tx)
		fsAcceptedCnt.Add(1)
		if staleRead[i] {
			fsStaleAcceptedCnt.Add(1)
		}
	}
	blk.Txs = out

	// prune nodes that left the window
	j := 0
	for j < len(f.nodes) && f.nodes[j].block < cutoff {
		f.nodes[j].pruned, f.nodes[j].succ = true, nil
		j++
	}
	f.nodes = f.nodes[j:]
}

// fsWithoutReads copies a transaction's namespaces for the validator-committer, dropping read versions.
func fsWithoutReads(nss []*applicationpb.TxNamespace) []*applicationpb.TxNamespace {
	out := make([]*applicationpb.TxNamespace, len(nss))
	for i, ns := range nss {
		c := &applicationpb.TxNamespace{NsId: ns.NsId, NsVersion: ns.NsVersion}
		c.BlindWrites = append(c.BlindWrites, ns.BlindWrites...)
		for _, rw := range ns.ReadWrites {
			c.BlindWrites = append(c.BlindWrites, &applicationpb.Write{Key: rw.Key, Value: rw.Value})
		}
		out[i] = c
	}
	return out
}

// fsVCNamespaces returns the namespaces the validator-committer receives for a transaction.
func fsVCNamespaces(tx *servicepb.TxWithRef) []*applicationpb.TxNamespace {
	if FabricSharpOn && tx.Ref != nil {
		if v, ok := fsStripped.LoadAndDelete(tx.Ref.TxId); ok {
			return v.([]*applicationpb.TxNamespace) //nolint:forcetypeassert
		}
	}
	return tx.Content.Namespaces
}
