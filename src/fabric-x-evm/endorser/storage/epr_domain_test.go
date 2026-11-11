package storage

// The endorser replica applies signed content exactly where the committer recognizes it:
// guarded deltas and programs on storage keys ("<ns>:str:...") only, and programs only when longer than a
// 32-byte storage word. Any other value is stored as written.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-evm/endorser/execution"
)

func eprDomainWord(x byte) []byte {
	w := make([]byte, 32)
	w[31] = x
	return w
}

// (i) a plain write to a non-storage key whose value is a valid program encoding naming the key itself and
// another key as outputs is stored as written, and the other key is unchanged.
func TestEPRLightKVSProgramEncodingOnNonStorageKeyIsPlain(t *testing.T) {
	const code, bal = "acc:0x00000000000000000000000000000000000c0de0:code", "str:0xa:0x1"
	p := &execution.EPRProgram{
		Keys:    []string{code, bal},
		Nodes:   []execution.EPRProgNode{{Op: execution.EPROpConst, A: -1, B: -1, C: -1, Const: []byte{99}}},
		Outputs: []execution.EPRProgOut{{Key: 0, Node: 0}, {Key: 1, Node: 0}},
	}
	enc := p.Encode()
	_, ok := execution.DecodeEPRProgram(enc)
	require.True(t, ok)
	old := map[string]*ValueVersion{"ns:" + bal: {Value: eprDomainWord(100), Version: 3}}
	got := applyUpdates(old, []KeyValueVersion{{Key: "ns:" + code, Value: enc, BlockNum: 1, TxID: "t1"}})
	require.Equal(t, enc, got["ns:"+code].Value, "stored as written")
	require.Equal(t, eprDomainWord(100), got["ns:"+bal].Value)
	require.EqualValues(t, 3, got["ns:"+bal].Version)
}

// (ii) a 32-byte storage word starting with "EPRP1" is stored as written, even when its bytes parse as a
// program (no keys, one 3-byte constant node, no guards, no outputs).
func TestEPRLightKVSStorageWordWithProgramTagIsPlain(t *testing.T) {
	word := append([]byte("EPRP1"), 0, 0, 0, 0, 0, 1, execution.EPROpConst,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 3, 'a', 'b', 'c', 0, 0, 0, 0)
	require.Len(t, word, 32)
	const slot = "ns:str:0xb:0x1"
	old := map[string]*ValueVersion{slot: {Value: eprDomainWord(7), Version: 1}}
	got := applyUpdates(old, []KeyValueVersion{{Key: slot, Value: word, BlockNum: 1, TxID: "t3"}})
	require.Equal(t, word, got[slot].Value)
	require.EqualValues(t, 2, got[slot].Version)
}

// Guarded deltas still apply on storage keys, and a delta-shaped value elsewhere is stored as written.
func TestEPRLightKVSDeltaOnlyOnStorageKeys(t *testing.T) {
	delta := append([]byte("EPRD2"), make([]byte, 25)...)
	delta[12] = 5 // delta = +5
	delta[29] = 32
	old := map[string]*ValueVersion{"ns:str:0xa:0x1": {Value: eprDomainWord(100)}, "ns:acc:0xa:code": {Value: []byte{1}}}
	got := applyUpdates(old, []KeyValueVersion{
		{Key: "ns:str:0xa:0x1", Value: delta, TxID: "t1"},
		{Key: "ns:acc:0xa:code", Value: delta, TxID: "t1"},
	})
	require.Equal(t, eprDomainWord(105), got["ns:str:0xa:0x1"].Value)
	require.Equal(t, delta, got["ns:acc:0xa:code"].Value)
}
