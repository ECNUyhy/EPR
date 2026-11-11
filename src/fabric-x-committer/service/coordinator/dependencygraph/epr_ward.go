/*
Baseline (not upstream): the WARD ordering of HTFabric (Song et al., CIKM'24), in the Fabric-X coordinator.
With FX_WARD=1 the transactions of every incoming batch (one block) are reordered before dependency
construction, without aborting any: write-conflict degree ascending, then read-conflict degree descending,
ties in the original order. Following the paper's Definitions 3.2-3.3 and its Figure 4 (its Algorithm 1 lists
the two degree tables the other way round), the in-degree of a key is the number of transactions writing it
and its out-degree the number reading it; wc(T) sums the out-degrees of T's write keys (how many transactions
T's writes can invalidate) and rc(T) the in-degrees of T's read keys (how many can invalidate T).
Combined with the parallel re-execution of d16reexec_v2 (-parallel), this is the HTFabric baseline.
*/

package dependencygraph

import (
	"os"
	"sort"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

var eprWardOn = os.Getenv("FX_WARD") == "1"

func eprWard(txs []*servicepb.TxWithRef) []*servicepb.TxWithRef {
	n := len(txs)
	if n < 2 {
		return txs
	}
	type rw struct{ reads, writes []string }
	sets := make([]rw, n)
	writers, readers := map[string]int{}, map[string]int{}
	for i, tx := range txs {
		if tx.Content == nil {
			continue
		}
		for _, ns := range tx.Content.Namespaces {
			for _, r := range ns.ReadsOnly {
				k := ns.NsId + "\x00" + string(r.Key)
				sets[i].reads = append(sets[i].reads, k)
				readers[k]++
			}
			for _, x := range ns.ReadWrites {
				k := ns.NsId + "\x00" + string(x.Key)
				sets[i].reads = append(sets[i].reads, k)
				sets[i].writes = append(sets[i].writes, k)
				readers[k]++
				writers[k]++
			}
			for _, w := range ns.BlindWrites {
				k := ns.NsId + "\x00" + string(w.Key)
				sets[i].writes = append(sets[i].writes, k)
				writers[k]++
			}
		}
	}
	wc, rc := make([]int, n), make([]int, n)
	for i := range sets {
		for _, k := range sets[i].writes {
			wc[i] += readers[k]
		}
		for _, k := range sets[i].reads {
			rc[i] += writers[k]
		}
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		i, j := idx[a], idx[b]
		if wc[i] != wc[j] {
			return wc[i] < wc[j] // write ascending
		}
		return rc[i] > rc[j] // read descending; stable sort keeps the original order on ties
	})
	out := make([]*servicepb.TxWithRef, n)
	for p, i := range idx {
		out[p] = txs[i]
	}
	return out
}
