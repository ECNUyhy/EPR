/*
Baseline (not upstream): Fabric++ (Sharma et al., SIGMOD'19) with the original scheduler shipped with
FabricSharp (fabricsharp/fppscheduler, ported unchanged; see port_orig_baselines.py). HTFabric (CIKM'24)
uses the same code as Fabric++, whose authors did not publish theirs.

Glue, the counterpart of the original orderer's blockcutter: every block is one cut. Its transactions go
through ScheduleTxn in block order with their (ns-qualified) read keys, the versions read and their write
keys; ProcessBlk returns the commit order. Transactions left out of it (cycle victims, or a block exceeding
the scheduler's key capacity) are aborted early; the scheduler's in-block "invalid" transactions (two
reads of different versions of a key) stay in the block, as in the original, and the validator decides.
Fabric-X versions are per-key counters: version v becomes {BlockNum: v+1}, an absent key {0, 0}, which keeps
the only property the scheduler uses (equality).
*/

package dependencygraph

import (
	"github.com/hyperledger/fabric-x-common/api/committerpb"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
	"github.com/hyperledger/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp/fppscheduler"
)

func eprFppOrig(txs []*servicepb.TxWithRef) ([]*servicepb.TxWithRef, map[int]bool) {
	if len(txs) < 2 {
		return txs, nil
	}
	s := fppscheduler.NewTxnScheduler(uint32(len(txs))) //nolint:gosec
	var passthrough []*servicepb.TxWithRef
	byID := make(map[string]*servicepb.TxWithRef, len(txs))
	var order []string // arrival order of the scheduled ones
	rejected := map[string]bool{}
	for _, tx := range txs {
		if tx.Content == nil || tx.Ref == nil {
			passthrough = append(passthrough, tx)
			continue
		}
		system := false
		var reads []fppscheduler.Read
		var writes []string
		for _, ns := range tx.Content.Namespaces {
			if ns.NsId == committerpb.MetaNamespaceID || ns.NsId == committerpb.ConfigNamespaceID {
				system = true
				break
			}
			for _, rw := range ns.ReadWrites {
				k := constructCompositeKey(ns.NsId, rw.Key)
				writes = append(writes, k)
				reads = append(reads, fppscheduler.Read{Key: k, Version: fppVersion(rw.Version)})
			}
			for _, w := range ns.BlindWrites {
				writes = append(writes, constructCompositeKey(ns.NsId, w.Key))
			}
			for _, r := range ns.ReadsOnly {
				reads = append(reads, fppscheduler.Read{Key: constructCompositeKey(ns.NsId, r.Key), Version: fppVersion(r.Version)})
			}
		}
		if system {
			passthrough = append(passthrough, tx) // non-endorser transactions are put ahead, as in the original
			continue
		}
		id := tx.Ref.TxId
		byID[id] = tx
		order = append(order, id)
		if !s.ScheduleTxn(reads, writes, 0, id) {
			rejected[id] = true
		}
	}
	schedule := s.ProcessBlk(0)
	res := make([]*servicepb.TxWithRef, 0, len(txs))
	res = append(res, passthrough...)
	in := make(map[string]bool, len(schedule))
	for _, id := range schedule {
		in[id] = true
		res = append(res, byID[id])
	}
	abortIdx := map[int]bool{}
	for _, id := range order {
		if !in[id] {
			abortIdx[len(res)] = true
			res = append(res, byID[id])
		}
	}
	eprReorderBatches.Add(1)
	eprReorderAborted.Add(int64(len(abortIdx)))
	return res, abortIdx
}

func fppVersion(v *uint64) *fppscheduler.Version {
	if v == nil {
		return &fppscheduler.Version{}
	}
	return &fppscheduler.Version{BlockNum: *v + 1}
}
