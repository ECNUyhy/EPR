package vc

// HTFabric baseline: the early-gossip split (epr_reexec.go).

import (
	"testing"

	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"
)

func TestEPREarlyGossipSplit(t *testing.T) {
	v := &validatedTransactions{
		validTxNonBlindWrites: transactionToWrites{},
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             transactionToWrites{},
		readToTxIDs:           readToTransactions{},
		invalidTxStatus:       map[TxID]committerpb.Status{"bad": committerpb.Status_ABORTED_MVCC_CONFLICT},
		txIDToHeight:          transactionIDToHeight{},
	}
	v.validTxNonBlindWrites.getOrCreate("valid", "ns").append([]byte("a"), []byte("1"), 1)
	v.validTxNonBlindWrites.getOrCreate("redo", "ns").append([]byte("b"), []byte("2"), 1)
	v.newWrites.getOrCreate("redo", "ns").append([]byte("c"), []byte("3"), 0)
	shared := newCmpRead("ns", []byte("r"), nil)
	v.readToTxIDs[shared] = []TxID{"valid", "redo"}
	v.readToTxIDs[newCmpRead("ns", []byte("b"), nil)] = []TxID{"redo"}

	lv, late := v.eprSplitEarlyGossip()
	require.Nil(t, lv, "nothing deferred: no split")
	require.Nil(t, late)

	eprLateWork.Store(v, &eprLate{txs: map[TxID]bool{"redo": true, "bad": true}})
	lv, late = v.eprSplitEarlyGossip()
	require.NotNil(t, lv)
	require.True(t, late.txs["redo"])
	require.Contains(t, v.validTxNonBlindWrites, TxID("valid"))
	require.NotContains(t, v.validTxNonBlindWrites, TxID("redo"))
	require.Contains(t, lv.validTxNonBlindWrites, TxID("redo"))
	require.Contains(t, lv.newWrites, TxID("redo"))
	require.Empty(t, v.newWrites)
	require.Equal(t, committerpb.Status_ABORTED_MVCC_CONFLICT, lv.invalidTxStatus["bad"])
	require.Empty(t, v.invalidTxStatus)
	require.Equal(t, []TxID{"valid"}, v.readToTxIDs[shared])
	require.Equal(t, []TxID{"redo"}, lv.readToTxIDs[shared])
	require.Len(t, v.readToTxIDs, 1)
	require.Len(t, lv.readToTxIDs, 2)
	_, again := eprLateWork.Load(v)
	require.False(t, again, "the deferred work is consumed")
}

func TestEPRApplyReexecWrites(t *testing.T) {
	v := &validatedTransactions{
		validTxNonBlindWrites: transactionToWrites{},
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             transactionToWrites{},
		readToTxIDs:           readToTransactions{},
		invalidTxStatus:       map[TxID]committerpb.Status{"guard": committerpb.Status_ABORTED_MVCC_CONFLICT},
		txIDToHeight:          transactionIDToHeight{},
	}
	// ok: endorsed storage write replaced, non-storage write kept, a new key from a changed path added
	v.validTxNonBlindWrites.getOrCreate("ok", "basic").append([]byte("str:a"), []byte("stale"), 3)
	v.validTxNonBlindWrites.getOrCreate("ok", "basic").append([]byte("acc:x:nonce"), []byte("n"), 1)
	// reverted: aborted
	v.validTxNonBlindWrites.getOrCreate("rev", "basic").append([]byte("str:b"), []byte("stale"), 2)
	// guard: EPR failed its guard (writes dropped, invalid); re-execution succeeds and decides
	// (its writes are gone, as updateInvalidTxs leaves them)
	all := map[TxID]string{"ok": "basic", "rev": "basic", "guard": "basic"}
	oks := map[TxID]bool{"ok": true, "rev": false, "guard": true}
	txw := map[TxID]map[string][]byte{
		"ok":    {"str:a": []byte("fresh"), "str:new": []byte("y")},
		"guard": {"str:c": []byte("z")},
	}
	v.eprApplyReexecWrites(all, oks, txw)

	ok := v.validTxNonBlindWrites["ok"]["basic"]
	require.Equal(t, [][]byte{[]byte("acc:x:nonce")}, ok.keys, "storage writes leave the non-blind set")
	bw := v.validTxBlindWrites["ok"]["basic"]
	require.Equal(t, [][]byte{[]byte("str:a"), []byte("str:new")}, bw.keys)
	require.Equal(t, [][]byte{[]byte("fresh"), []byte("y")}, bw.values)
	require.Equal(t, committerpb.Status_ABORTED_MVCC_CONFLICT, v.invalidTxStatus["rev"])
	require.NotContains(t, v.validTxNonBlindWrites, TxID("rev"))
	require.NotContains(t, v.invalidTxStatus, TxID("guard"))
	require.Equal(t, [][]byte{[]byte("str:c")}, v.validTxBlindWrites["guard"]["basic"].keys)
}

func TestEPRNoStorageWrites(t *testing.T) {
	v := &validatedTransactions{
		validTxNonBlindWrites: transactionToWrites{},
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             transactionToWrites{},
	}
	v.validTxNonBlindWrites.getOrCreate("query", "basic").append([]byte("acc:0x1:nonce"), []byte("1"), 1)
	v.validTxNonBlindWrites.getOrCreate("transfer", "basic").append([]byte("acc:0x2:nonce"), []byte("1"), 1)
	v.newWrites.getOrCreate("transfer", "basic").append([]byte("str:0xa:0x1"), []byte("5"), 0)
	require.True(t, v.eprNoStorageWrites([]TxID{"query"}), "a nonce write alone is not a storage write")
	require.False(t, v.eprNoStorageWrites([]TxID{"transfer"}))
	require.False(t, v.eprNoStorageWrites([]TxID{"query", "transfer"}), "every reader must be read-only")
	require.False(t, v.eprNoStorageWrites(nil))
	// a write to another account field (a balance) disqualifies it as well
	v.validTxBlindWrites.getOrCreate("paying", "basic").append([]byte("acc:0x3:nonce"), []byte("1"), 1)
	v.validTxNonBlindWrites.getOrCreate("paying", "basic").append([]byte("acc:0x3:bal"), []byte("9"), 1)
	require.False(t, v.eprNoStorageWrites([]TxID{"paying"}))
}
