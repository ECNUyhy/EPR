/*
Baseline (not upstream): Fabric++ in-block reordering with early abort (Sharma et al., SIGMOD'19), in the
Fabric-X coordinator. FX_REORDER=1 runs the original Fabric++ scheduler shipped with FabricSharp
(epr_fpp_orig.go; the authors of Fabric++ did not publish theirs). FX_REORDER=own runs the
reimplementation below, where the transactions of every incoming batch (one block) are reordered before
dependency construction:

  1. conflict graph: Ti -> Tj (Ti must precede Tj) if Ti reads a key that Tj writes; placing a reader
     after a writer of the same block would make its read stale;
  2. cycle breaking: within every strongly connected component with more than one transaction, the
     transaction with the most conflict edges inside the component is aborted early (ties: the later one)
     until no cycle remains;
  3. the remaining transactions are topologically sorted (ties: original order); aborted ones keep their
     position and carry ABORTED_MVCC_CONFLICT, as a validator would assign.

The procedure is deterministic, so all committers reorder identically.
*/

package dependencygraph

import (
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-x-common/api/committerpb"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

var (
	eprReorderOn                         = os.Getenv("FX_REORDER") == "1" || eprReorderOwn
	eprReorderOwn                        = os.Getenv("FX_REORDER") == "own"
	eprReorderAborted, eprReorderBatches atomic.Int64
)

func init() {
	if !eprReorderOn {
		return
	}
	go func() {
		for range time.Tick(5 * time.Second) {
			logger.Infof("PILOT_REORDER batches=%d early_aborted=%d", eprReorderBatches.Load(), eprReorderAborted.Load())
		}
	}()
}

// eprReorder returns the batch's transactions in the order Fabric++ would commit them and the indices
// (into the returned slice) of the transactions it aborts early.
func eprReorder(txs []*servicepb.TxWithRef) ([]*servicepb.TxWithRef, map[int]bool) {
	if !eprReorderOwn {
		return eprFppOrig(txs)
	}
	n := len(txs)
	if n < 2 {
		return txs, nil
	}
	reads := make([]map[string]bool, n)
	writes := make([]map[string]bool, n)
	readers := map[string][]int{}
	for i, tx := range txs {
		reads[i], writes[i] = map[string]bool{}, map[string]bool{}
		if tx.Content == nil {
			continue
		}
		for _, ns := range tx.Content.Namespaces {
			for _, r := range ns.ReadsOnly {
				reads[i][constructCompositeKey(ns.NsId, r.Key)] = true
			}
			for _, rw := range ns.ReadWrites {
				k := constructCompositeKey(ns.NsId, rw.Key)
				reads[i][k] = true
				writes[i][k] = true
			}
			for _, w := range ns.BlindWrites {
				writes[i][constructCompositeKey(ns.NsId, w.Key)] = true
			}
		}
		for k := range reads[i] {
			readers[k] = append(readers[k], i)
		}
	}
	out := make([]map[int]bool, n) // i -> j: i must precede j
	in := make([]map[int]bool, n)
	for i := range out {
		out[i], in[i] = map[int]bool{}, map[int]bool{}
	}
	for j := range txs {
		for k := range writes[j] {
			for _, i := range readers[k] {
				if i != j {
					out[i][j], in[j][i] = true, true
				}
			}
		}
	}
	alive := make([]bool, n)
	for i := range alive {
		alive[i] = true
	}
	aborted := map[int]bool{}
	for {
		comps := eprSCC(n, alive, out)
		cyclic := false
		for _, c := range comps {
			if len(c) < 2 {
				continue
			}
			cyclic = true
			inC := map[int]bool{}
			for _, v := range c {
				inC[v] = true
			}
			victim, best := -1, -1
			for _, v := range c {
				d := 0
				for u := range out[v] {
					if inC[u] {
						d++
					}
				}
				for u := range in[v] {
					if inC[u] {
						d++
					}
				}
				if d > best || (d == best && v > victim) {
					victim, best = v, d
				}
			}
			alive[victim] = false
			aborted[victim] = true
		}
		if !cyclic {
			break
		}
	}
	// Kahn's algorithm over the surviving transactions, ties broken by original position
	indeg := make([]int, n)
	for i := range txs {
		if !alive[i] {
			continue
		}
		for u := range in[i] {
			if alive[u] {
				indeg[i]++
			}
		}
	}
	var ready []int
	for i := range txs {
		if alive[i] && indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	order := make([]int, 0, n)
	for len(ready) > 0 {
		sort.Ints(ready)
		v := ready[0]
		ready = ready[1:]
		order = append(order, v)
		for u := range out[v] {
			if alive[u] {
				indeg[u]--
				if indeg[u] == 0 {
					ready = append(ready, u)
				}
			}
		}
	}
	res := make([]*servicepb.TxWithRef, 0, n)
	abortIdx := map[int]bool{}
	for _, v := range order {
		res = append(res, txs[v])
	}
	idx := make([]int, 0, len(aborted))
	for v := range aborted {
		idx = append(idx, v)
	}
	sort.Ints(idx)
	for _, v := range idx {
		abortIdx[len(res)] = true
		res = append(res, txs[v])
	}
	eprReorderBatches.Add(1)
	eprReorderAborted.Add(int64(len(aborted)))
	return res, abortIdx
}

// eprSCC returns the strongly connected components of the alive vertices (Tarjan, iterative order by index).
func eprSCC(n int, alive []bool, out []map[int]bool) [][]int {
	index, low := make([]int, n), make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	var comps [][]int
	next := 0
	var strong func(v int)
	strong = func(v int) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		succ := make([]int, 0, len(out[v]))
		for u := range out[v] {
			succ = append(succ, u)
		}
		sort.Ints(succ)
		for _, u := range succ {
			if !alive[u] {
				continue
			}
			if index[u] == -1 {
				strong(u)
				low[v] = min(low[v], low[u])
			} else if onStack[u] {
				low[v] = min(low[v], index[u])
			}
		}
		if low[v] == index[v] {
			var c []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				c = append(c, w)
				if w == v {
					break
				}
			}
			comps = append(comps, c)
		}
	}
	for v := 0; v < n; v++ {
		if alive[v] && index[v] == -1 {
			strong(v)
		}
	}
	return comps
}

// eprEarlyAbortStatus is the status of a transaction Fabric++ aborts early.
var eprEarlyAbortStatus = committerpb.Status_ABORTED_MVCC_CONFLICT
