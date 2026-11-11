/*
Baseline (not upstream): post-order re-execution at the validator-committer, XOX / HTFabric / ReX style.

With FX_REEXEC_URL set, the VC re-executes every transaction whose stale reads it would otherwise repair:
it sends the raw transactions of the batch, in ledger order, to a colocated EVM service (d16reexec_v2) that
executes them against the latest committed state in this database, and waits for the result on the
commit path. Endorsers attach each raw transaction as a blind write to a unique key "evmtx:<hash>"
(a re-executing committer needs the transaction input, as in XOX); the VC strips these writes before
commit. The committed values are cross-checked against the EPR evaluation of the same transactions: by
Lemmas 1-2 they must be equal, and mismatches are counted.

Early gossip (HTFabric, CIKM'24, Sec. 4.2; FX_EARLY_GOSSIP=1): the validator first passes on the batch
without the re-executed transactions, which the committer commits (and so makes visible to endorsers) at
once, then re-executes and passes on the rest. The two parts share no key: a batch the coordinator dispatches
holds no conflicting transactions.

Re-executing every invalid transaction (FX_REEXEC_ALL=1, XOX and HTFabric): a transaction with a stale read
that EPR cannot repair is not aborted but re-executed too, and its re-executed storage writes replace its
endorsed ones (as blind writes: the committer fetches their versions); a reverted re-execution aborts it.
Without it, only the transactions EPR repairs are re-executed (cost only) and the rest abort.
*/

package vc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-x-common/api/committerpb"
)

var (
	eprReexecAll   = os.Getenv("FX_REEXEC_ALL") == "1"
	eprEarlyGossip = os.Getenv("FX_EARLY_GOSSIP") == "1"
	eprLateWork    sync.Map // *validatedTransactions -> *eprLate (early gossip: re-execution deferred)
	eprReexecURL   = os.Getenv("FX_REEXEC_URL")
	eprRawTxPrefix = []byte("evmtx:")
	eprReexecHTTP  = &http.Client{Timeout: 60 * time.Second}

	eprReexecCalls, eprReexecTxs, eprReexecMicros atomic.Int64
	eprReexecCompared, eprReexecMismatch          atomic.Int64
	// per-transaction outcome: re-execution succeeded iff EPR committed the transaction
	eprReexecTxCompared, eprReexecTxMismatch atomic.Int64
)

// eprStripRawTxs removes the raw-transaction carrier writes from every write set and returns them per tx.
func (v *validatedTransactions) eprStripRawTxs() map[TxID][]byte {
	out := map[TxID][]byte{}
	for _, tw := range []transactionToWrites{v.validTxNonBlindWrites, v.validTxBlindWrites, v.newWrites} {
		for tx, nsw := range tw {
			for _, w := range nsw {
				for i := 0; i < len(w.keys); {
					if bytes.HasPrefix(w.keys[i], eprRawTxPrefix) {
						out[tx] = w.values[i]
						w.keys = append(w.keys[:i], w.keys[i+1:]...)
						w.values = append(w.values[:i], w.values[i+1:]...)
						w.versions = append(w.versions[:i], w.versions[i+1:]...)
						continue
					}
					i++
				}
			}
		}
	}
	return out
}

// eprReexecute re-executes the given transactions (ledger order) through the colocated EVM service and
// returns the final value of every storage key they wrote and, per transaction, whether it succeeded.
func (v *validatedTransactions) eprReexecute(ctx context.Context, txs map[TxID]bool, raws map[TxID][]byte) (map[string][]byte, map[TxID]bool, map[TxID]map[string][]byte, error) {
	order := make([]TxID, 0, len(txs))
	for tx := range txs {
		if raws[tx] != nil {
			order = append(order, tx)
		}
	}
	if len(order) == 0 {
		return nil, nil, nil, nil
	}
	sort.Slice(order, func(a, b int) bool {
		ha, hb := v.txIDToHeight[order[a]], v.txIDToHeight[order[b]]
		if ha == nil || hb == nil {
			return string(order[a]) < string(order[b])
		}
		if ha.BlockNum != hb.BlockNum {
			return ha.BlockNum < hb.BlockNum
		}
		return ha.TxNum < hb.TxNum
	})
	req := struct {
		Txs    [][]byte   `json:"txs"`
		Keys   []string   `json:"keys"`    // endorsed read keys of these txs: the service prefetches them in one query
		TxKeys [][]string `json:"tx_keys"` // per transaction (request order): its endorsed read keys (HTFabric's TSP)
	}{}
	pos := make(map[TxID]int, len(order))
	for i, tx := range order {
		req.Txs = append(req.Txs, raws[tx])
		pos[tx] = i
	}
	req.TxKeys = make([][]string, len(order))
	seenKey := map[string]bool{}
	for cr, ids := range v.readToTxIDs {
		for _, id := range ids {
			if i, ok := pos[id]; ok {
				req.TxKeys[i] = append(req.TxKeys[i], cr.key)
				if !seenKey[cr.key] {
					seenKey[cr.key] = true
					req.Keys = append(req.Keys, cr.key)
				}
			}
		}
	}
	body, _ := json.Marshal(req)
	start := time.Now()
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, eprReexecURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, nil, err
	}
	resp, err := eprReexecHTTP.Do(hr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reexec: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Final    map[string][]byte   `json:"final"`
		OK       []bool              `json:"ok"`
		TxWrites []map[string][]byte `json:"tx_writes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, nil, fmt.Errorf("reexec decode: %w", err)
	}
	oks := make(map[TxID]bool, len(order))
	txw := make(map[TxID]map[string][]byte, len(order))
	for i, tx := range order {
		if i < len(out.OK) {
			oks[tx] = out.OK[i]
		}
		if i < len(out.TxWrites) {
			txw[tx] = out.TxWrites[i]
		}
	}
	eprReexecCalls.Add(1)
	eprReexecTxs.Add(int64(len(order)))
	eprReexecMicros.Add(time.Since(start).Microseconds())
	return out.Final, oks, txw, nil
}

// eprReexecCompare counts keys whose re-executed value differs from EPR's evaluated value.
func eprReexecCompare(final map[string][]byte, epr map[string][]byte) {
	for k, rv := range final {
		ev, ok := epr[k]
		if !ok {
			continue
		}
		eprReexecCompared.Add(1)
		if !bytes.Equal(bytes.TrimLeft(rv, "\x00"), bytes.TrimLeft(ev, "\x00")) {
			eprReexecMismatch.Add(1)
		}
	}
}

// EPRReexecStats reports the re-execution baseline counters.
func EPRReexecStats() (calls, txs, micros, compared, mismatch int64) {
	return eprReexecCalls.Load(), eprReexecTxs.Load(), eprReexecMicros.Load(), eprReexecCompared.Load(), eprReexecMismatch.Load()
}

// eprReexecCompareTxs counts transactions whose re-execution outcome differs from EPR's validity decision.
func eprReexecCompareTxs(oks map[TxID]bool, failed map[TxID]bool) {
	for tx, ok := range oks {
		eprReexecTxCompared.Add(1)
		if ok == failed[tx] {
			eprReexecTxMismatch.Add(1)
		}
	}
}

// EPRReexecTxStats reports the per-transaction outcome comparison.
func EPRReexecTxStats() (compared, mismatch int64) {
	return eprReexecTxCompared.Load(), eprReexecTxMismatch.Load()
}

// eprLate is the deferred re-execution of a batch under early gossip.
type eprLate struct {
	txs  map[TxID]bool
	raws map[TxID][]byte
	all  map[TxID]string // committed from re-execution (FX_REEXEC_ALL): tx -> namespace
}

// eprSplitEarlyGossip moves the transactions whose re-execution was deferred out of v into a second batch,
// which it returns with the deferred work (nil if there is none).
func (v *validatedTransactions) eprSplitEarlyGossip() (*validatedTransactions, *eprLate) {
	w, ok := eprLateWork.LoadAndDelete(v)
	if !ok {
		return nil, nil
	}
	late := w.(*eprLate) //nolint:forcetypeassert
	lv := &validatedTransactions{
		validTxNonBlindWrites: transactionToWrites{},
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             transactionToWrites{},
		readToTxIDs:           readToTransactions{},
		invalidTxStatus:       map[TxID]committerpb.Status{},
		txIDToHeight:          v.txIDToHeight,
		eprCredits:            map[TxID][]eprCreditEntry{},
	}
	moveW := func(from, to transactionToWrites) {
		for tx, nsw := range from {
			if late.txs[tx] {
				to[tx] = nsw
				delete(from, tx)
			}
		}
	}
	moveW(v.validTxNonBlindWrites, lv.validTxNonBlindWrites)
	moveW(v.validTxBlindWrites, lv.validTxBlindWrites)
	moveW(v.newWrites, lv.newWrites)
	for tx, st := range v.invalidTxStatus {
		if late.txs[tx] {
			lv.invalidTxStatus[tx] = st
			delete(v.invalidTxStatus, tx)
		}
	}
	for tx, c := range v.eprCredits {
		if late.txs[tx] {
			lv.eprCredits[tx] = c
			delete(v.eprCredits, tx)
		}
	}
	for cr, ids := range v.readToTxIDs {
		var early, rest []TxID
		for _, id := range ids {
			if late.txs[id] {
				rest = append(rest, id)
			} else {
				early = append(early, id)
			}
		}
		if len(rest) > 0 {
			lv.readToTxIDs[cr] = rest
			if len(early) > 0 {
				v.readToTxIDs[cr] = early
			} else {
				delete(v.readToTxIDs, cr)
			}
		}
	}
	return lv, late
}

// eprRunLate re-executes the deferred transactions of the batch on the commit path.
func (v *validatedTransactions) eprRunLate(ctx context.Context, late *eprLate) error {
	_, oks, txw, err := v.eprReexecute(ctx, late.txs, late.raws)
	eprEarlyGossipBatches.Add(1)
	if err == nil && len(late.all) > 0 {
		v.eprApplyReexecWrites(late.all, oks, txw)
	}
	return err
}

var eprEarlyGossipBatches atomic.Int64

var eprReexecAllCommitted, eprReexecAllReverted, eprReexecAllNewKeys atomic.Int64

// EPRReexecAllStats reports the transactions committed from re-execution, the reverted ones, and the
// re-executed storage writes to keys the endorsed write set did not have (a changed execution path).
func EPRReexecAllStats() (committed, reverted, newKeys int64) {
	return eprReexecAllCommitted.Load(), eprReexecAllReverted.Load(), eprReexecAllNewKeys.Load()
}

// eprApplyReexecWrites commits the re-executed outcome of the given transactions: their storage writes
// (endorsed or EPR-evaluated) are replaced by the re-executed ones, as blind writes whose versions the
// committer fetches; a transaction whose re-execution reverted is aborted.
func (v *validatedTransactions) eprApplyReexecWrites(all map[TxID]string, oks map[TxID]bool, txw map[TxID]map[string][]byte) {
	storage := []byte("str:")
	var reverted []TxID
	for tx, ns := range all { // the namespace of its conflicting read: EPR may already have dropped its writes
		endorsed := map[string]bool{}
		for _, tw := range []transactionToWrites{v.validTxNonBlindWrites, v.validTxBlindWrites, v.newWrites} {
			for _, w := range tw[tx] {
				for i := 0; i < len(w.keys); {
					if bytes.HasPrefix(w.keys[i], storage) {
						endorsed[string(w.keys[i])] = true
						w.keys = append(w.keys[:i], w.keys[i+1:]...)
						w.values = append(w.values[:i], w.values[i+1:]...)
						w.versions = append(w.versions[:i], w.versions[i+1:]...)
						continue
					}
					i++
				}
			}
		}
		if !oks[tx] || ns == "" {
			reverted = append(reverted, tx)
			continue
		}
		delete(v.invalidTxStatus, tx) // EPR may have failed its guard; re-execution decides
		keys := make([]string, 0, len(txw[tx]))
		for k := range txw[tx] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !endorsed[k] {
				eprReexecAllNewKeys.Add(1)
			}
			v.validTxBlindWrites.getOrCreate(tx, ns).append([]byte(k), txw[tx][k], 0)
		}
		eprReexecAllCommitted.Add(1)
	}
	if len(reverted) > 0 {
		eprReexecAllReverted.Add(int64(len(reverted)))
		v.updateInvalidTxs(reverted, committerpb.Status_ABORTED_MVCC_CONFLICT)
	}
}
