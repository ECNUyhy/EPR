package vc

// Domain separation of signed content in the VC. EVM signed content (EPRD2 deltas, EPRP1
// programs) is decoded on storage keys ("str:...") only, a value of at most 32 bytes is never a program, and a
// program may set exactly the keys whose writes carry it. The committed values are served as prefetched, so
// these tests need no database.

import (
	"context"
	"math"
	"testing"

	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"
)

const eprTestNs = "basic"

func eprTestWord(x int64) []byte { return eprEncodeWidth(x, 32) }

func eprTestDelta(d, lower, upper int64) []byte {
	b := append([]byte{}, eprMagic2...)
	for _, x := range []int64{d, lower, upper} {
		b = append(b, eprEncodeBalance(x)...)
	}
	return append(b, 32)
}

// eprConstProgram encodes a well-formed program setting every key of outs to the constant c (with endorsed
// values, so that it is eligible for the fast path).
func eprConstProgram(c byte, outs ...string) []byte {
	p := &EPRProgram{Keys: outs, Nodes: []EPRProgNode{{Op: EPROpConst, A: -1, B: -1, C: -1, Const: []byte{c}}}}
	for i := range outs {
		p.Outputs = append(p.Outputs, EPRProgOut{Key: uint16(i), Node: 0}) //nolint:gosec
		p.Endorsed = append(p.Endorsed, eprTestWord(int64(c)))
	}
	return p.Encode()
}

// eprRunBatch runs the VC's EPR evaluation on one batch. committed holds the committed values; keys lists
// every key the evaluation may look up (all of them count as queried by the prefetch).
func eprRunBatch(
	t *testing.T, fast bool, nb, nw transactionToWrites, committed map[string]eprState, keys ...string,
) *validatedTransactions {
	t.Helper()
	oldOn, oldFast := eprEnabled, eprFastPath
	eprEnabled, eprFastPath = true, fast
	t.Cleanup(func() { eprEnabled, eprFastPath = oldOn, oldFast })
	pf := &eprPrefetched{state: map[string]eprState{}, queried: map[string]bool{}, done: make(chan bool)}
	close(pf.done)
	for _, k := range keys {
		pf.queried[eprTestNs+"\x00"+k] = true
		if st, ok := committed[k]; ok {
			pf.state[eprTestNs+"\x00"+k] = st
		}
	}
	v := &validatedTransactions{
		validTxNonBlindWrites: nb,
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             nw,
		invalidTxStatus:       map[TxID]committerpb.Status{},
		eprPreWait:            func() *eprPrefetched { return pf },
	}
	_, err := v.pilotRepairDeltas(context.Background(), nil, namespaceToReads{})
	require.NoError(t, err)
	return v
}

func eprWriteOf(tw transactionToWrites, tx TxID, key string) (val []byte, ver uint64, ok bool) {
	w, found := tw[tx][eprTestNs]
	if !found {
		return nil, 0, false
	}
	for i, k := range w.keys {
		if string(k) == key {
			return w.values[i], w.versions[i], true
		}
	}
	return nil, 0, false
}

// (i) A plain write to a non-storage key (here contract code, whose bytes a deployer chooses) whose value is a
// valid program encoding naming the key itself and a balance slot as outputs commits as the plain value, and
// the slot is unchanged: a later delta in the same batch applies to its committed value.
func TestEPRProgramEncodingOnNonStorageKeyIsPlain(t *testing.T) {
	const code, bal = "acc:0x00000000000000000000000000000000000c0de0:code", "str:0xa:0x1"
	enc := eprConstProgram(99, code, bal)
	_, ok := DecodeEPRProgram(enc)
	require.True(t, ok, "the value is a well-formed program encoding")
	nw := transactionToWrites{}
	nw.getOrCreate("t1", eprTestNs).append([]byte(code), enc, 0)
	nb := transactionToWrites{}
	nb.getOrCreate("t2", eprTestNs).append([]byte(bal), eprTestDelta(5, 0, math.MaxInt64), 4)
	v := eprRunBatch(t, false, nb, nw, map[string]eprState{bal: {value: eprTestWord(100), version: 3}}, code, bal)
	require.Empty(t, v.invalidTxStatus)
	val, ver, ok := eprWriteOf(v.newWrites, "t1", code)
	require.True(t, ok)
	require.Equal(t, enc, val, "committed as the plain value written")
	require.Zero(t, ver)
	_, _, inNew := eprWriteOf(v.newWrites, "t1", bal)
	_, _, inOld := eprWriteOf(v.validTxNonBlindWrites, "t1", bal)
	require.False(t, inNew || inOld, "the plain write sets no other key")
	val, ver, ok = eprWriteOf(v.validTxNonBlindWrites, "t2", bal)
	require.True(t, ok)
	require.Equal(t, eprTestWord(105), val, "the delta applies to the committed value of the slot")
	require.EqualValues(t, 4, ver)
}

// (ii) A 32-byte storage word starting with "EPRP1" commits as a plain value, even when its bytes parse as a
// program (here: no keys, one 3-byte constant node, no guards, no outputs).
func TestEPRStorageWordWithProgramTagIsPlain(t *testing.T) {
	word := append([]byte("EPRP1"), 0, 0, 0, 0, 0, 1, EPROpConst,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 3, 'a', 'b', 'c', 0, 0, 0, 0)
	require.Len(t, word, 32)
	_, ok := DecodeEPRProgram(word)
	require.False(t, ok)
	const slot = "str:0xb:0x1"
	for _, fast := range []bool{false, true} {
		nb := transactionToWrites{}
		nb.getOrCreate("t3", eprTestNs).append([]byte(slot), word, 2)
		v := eprRunBatch(t, fast, nb, transactionToWrites{}, map[string]eprState{slot: {value: eprTestWord(7), version: 1}}, slot)
		require.Empty(t, v.invalidTxStatus, "fast=%v", fast)
		val, ver, ok := eprWriteOf(v.validTxNonBlindWrites, "t3", slot)
		require.True(t, ok)
		require.Equal(t, word, val)
		require.EqualValues(t, 2, ver)
	}
}

// A program whose outputs are not exactly the writes carrying it (here it also sets a slot the transaction does
// not write) is refused, on the fast path as on the evaluated one.
func TestEPRProgramOutputsMustEqualItsWrites(t *testing.T) {
	const a, b = "str:0xc:0x1", "str:0xa:0x1"
	enc := eprConstProgram(99, a, b)
	committed := map[string]eprState{a: {value: eprTestWord(1), version: 0}, b: {value: eprTestWord(100), version: 3}}
	for _, fast := range []bool{false, true} {
		nb := transactionToWrites{}
		nb.getOrCreate("t4", eprTestNs).append([]byte(a), enc, 1)
		v := eprRunBatch(t, fast, nb, transactionToWrites{}, committed, a, b)
		require.Contains(t, v.invalidTxStatus, TxID("t4"), "fast=%v", fast)
	}
	// the same program written to both of its outputs is accepted
	nb := transactionToWrites{}
	nb.getOrCreate("t5", eprTestNs).append([]byte(a), enc, 1)
	nb.getOrCreate("t5", eprTestNs).append([]byte(b), enc, 4)
	v := eprRunBatch(t, false, nb, transactionToWrites{}, committed, a, b)
	require.Empty(t, v.invalidTxStatus)
	for _, k := range []string{a, b} {
		val, _, ok := eprWriteOf(v.validTxNonBlindWrites, "t5", k)
		require.True(t, ok)
		require.Equal(t, eprTestWord(99), val)
	}
}
