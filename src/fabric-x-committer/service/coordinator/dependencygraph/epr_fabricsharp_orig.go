/*
Baseline (not upstream): FabricSharp (Ruan et al., SIGMOD'20) with the authors' scheduler
(fabricsharp/sharpscheduler and fabricsharp/common, branch sigmod20, ported unchanged; see
port_orig_baselines.py): Bloom-filter reachability with relay filters, LevelDB indices of committed reads
and writes, max_span = TXN_SPAN_LIMIT (default 10, as in the paper's experiments). FX_FABRICSHARP=1
selects it; FX_FABRICSHARP=exact keeps the idealized reimplementation of epr_fabricsharp.go.

Glue, the counterpart of the original orderer's blockcutter (scheduleMsg / Cut): every block is one cut.
Its transactions go through ProcessTxn in block order and ProcessBlk returns the commit order. A transaction
ProcessTxn refuses (snapshot older than max_span, a cycle, or a Bloom false positive between pending
transactions, "DESERT") is aborted early with ABORTED_MVCC_CONFLICT. Non-endorser transactions (system
namespaces) are put ahead, as in the original.

Snapshot. FabricSharp's peers record, as the block number of every read version, the height of the snapshot
the transaction was simulated against (snapshotbasedtxmgr/helper.go); the orderer takes it as the
transaction's snapshot. Fabric-X versions are per-key counters, so the endorser carries its snapshot height
in a blind write to "fssnap:<tx hash>" (FX_FS_SNAP=1, code/evm/patch_fs_snapshot.py), which is stripped
before validation. The endorser reads its height before it opens its state snapshot, which can therefore be
newer; the snapshot used is the larger of that height and the commit block of every version read. Both
bound the true snapshot from below, and no read key was written between them and the true snapshot (the
version read would be newer), so the dependencies are those of the true snapshot.
*/

package dependencygraph

import (
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
	"github.com/hyperledger/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp/sharpscheduler"
)

const fsSnapPrefix = "fssnap:"

var (
	fsOrig                                   *sharpscheduler.TxnScheduler
	fsOrigSpan                               = fsOrigSpanLimit()
	fsHist                                   = map[string]*fsVerHist{}
	fsSpanDroppedCnt, fsNoSnapCnt, fsLastBlk atomic.Int64
)

// fsOrigSpanLimit is the original's GetFSharpTxnSpanLimitWithDefault.
func fsOrigSpanLimit() uint64 {
	if limit, err := strconv.Atoi(os.Getenv("TXN_SPAN_LIMIT")); err == nil && limit > 0 {
		return uint64(limit)
	}
	return 10
}

// fsVerHist maps the versions of a key to the blocks that committed them: version base+1+i was committed in
// blocks[i]; older versions count as committed in block 0.
type fsVerHist struct {
	base   int64
	blocks []uint64
}

func (h *fsVerHist) blockOf(v int64) uint64 {
	if i := v - h.base - 1; i >= 0 && i < int64(len(h.blocks)) {
		return h.blocks[i]
	}
	return 0
}

// fsOrigTx extracts what the original's scheduleMsg extracts from a Fabric envelope.
func fsOrigTx(tx *servicepb.TxWithRef) (reads map[string]int64, writes []string, snap int64, ok bool) {
	reads, snap = map[string]int64{}, -1
	if tx.Content == nil || tx.Ref == nil {
		return nil, nil, 0, false
	}
	for _, ns := range tx.Content.Namespaces {
		if ns.NsId == committerpb.MetaNamespaceID || ns.NsId == committerpb.ConfigNamespaceID {
			return nil, nil, 0, false
		}
		for _, r := range ns.ReadsOnly {
			reads[fsOrigKey(ns.NsId, r.Key)] = fsVer(r.Version)
		}
		for _, rw := range ns.ReadWrites {
			k := fsOrigKey(ns.NsId, rw.Key)
			reads[k] = fsVer(rw.Version)
			writes = append(writes, k)
		}
		for _, w := range ns.BlindWrites {
			if strings.HasPrefix(string(w.Key), fsSnapPrefix) {
				if h, err := strconv.ParseInt(string(w.Value), 10, 64); err == nil {
					snap = h
				}
				continue
			}
			writes = append(writes, fsOrigKey(ns.NsId, w.Key))
		}
	}
	return reads, writes, snap, true
}

// fsOrigKey qualifies a key with its namespace for the original scheduler, whose LevelDB indices use 0x00 as
// separator (Fabric chaincode keys contain none): keys containing 0x00 are hex-encoded.
func fsOrigKey(ns string, k []byte) string {
	if bytes.IndexByte(k, 0) < 0 {
		return ns + "/" + string(k)
	}
	return ns + "/0x" + hex.EncodeToString(k)
}

func fabricSharpOrigBlock(blk *servicepb.CoordinatorBatch) {
	blockNum := blk.Txs[0].Ref.BlockNum
	if fsOrig == nil {
		fsOrig = sharpscheduler.NewTxnScheduler(fsOrigSpan, "/tmp/d16_fabricsharp_store")
	}
	fsLastBlk.Store(int64(blockNum)) //nolint:gosec
	var passthrough []*servicepb.TxWithRef
	pending := make(map[string]*servicepb.TxWithRef, len(blk.Txs))
	pendingWrites := make(map[string][]string, len(blk.Txs))
	for _, tx := range blk.Txs {
		reads, writes, snap, ok := fsOrigTx(tx)
		if !ok {
			passthrough = append(passthrough, tx)
			continue
		}
		readKeys := make([]string, 0, len(reads))
		snapshot := blockNum // no read keys: the original sets the snapshot to the next block height
		if len(reads) > 0 {
			if snap < 0 {
				fsNoSnapCnt.Add(1)
				snap = 0
			}
			s := uint64(snap) //nolint:gosec
			for k, v := range reads {
				readKeys = append(readKeys, k)
				h := fsHist[k]
				if h == nil || v > h.base+int64(len(h.blocks)) { // first sight, or versions we did not see
					fsHist[k] = &fsVerHist{base: v}
					continue
				}
				if b := h.blockOf(v); b > s {
					s = b
				}
			}
			snapshot = s
		}
		if fsOrigSpan < blockNum && snapshot < blockNum-fsOrigSpan {
			fsSpanDroppedCnt.Add(1)
		}
		if fsOrig.ProcessTxn(readKeys, writes, snapshot, blockNum, tx.Ref.TxId) {
			pending[tx.Ref.TxId] = tx
			pendingWrites[tx.Ref.TxId] = writes
			continue
		}
		fsAbortedCnt.Add(1)
		blk.Rejected = append(blk.Rejected, &committerpb.TxStatus{
			Ref: tx.Ref, Status: committerpb.Status_ABORTED_MVCC_CONFLICT,
		})
	}
	schedule := fsOrig.ProcessBlk(blockNum)
	out := make([]*servicepb.TxWithRef, 0, len(passthrough)+len(schedule))
	out = append(out, passthrough...)
	for _, id := range schedule {
		tx := pending[id]
		for _, k := range pendingWrites[id] { // every scheduled write commits (no concurrency validation)
			if h := fsHist[k]; h != nil {
				h.blocks = append(h.blocks, blockNum)
				if len(h.blocks) > 64 && h.blocks[0]+fsOrigSpan+2 < blockNum { // blocks before the span no longer matter
					i := 0
					for i < len(h.blocks) && h.blocks[i]+fsOrigSpan+2 < blockNum {
						i++
					}
					h.base += int64(i)
					h.blocks = append([]uint64(nil), h.blocks[i:]...)
				}
			}
		}
		fsStripped.Store(id, fsOrigVCNamespaces(tx.Content.Namespaces))
		out = append(out, tx)
		fsAcceptedCnt.Add(1)
	}
	blk.Txs = out
}

// fsOrigVCNamespaces copies a transaction's namespaces for the validator-committer without read versions
// (FabricSharp validates no concurrency at peers) and without the snapshot carrier.
func fsOrigVCNamespaces(nss []*applicationpb.TxNamespace) []*applicationpb.TxNamespace {
	out := fsWithoutReads(nss)
	for _, ns := range out {
		w := ns.BlindWrites[:0]
		for _, x := range ns.BlindWrites {
			if !strings.HasPrefix(string(x.Key), fsSnapPrefix) {
				w = append(w, x)
			}
		}
		ns.BlindWrites = w
	}
	return out
}
