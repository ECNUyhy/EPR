package execution

// Determinism of the program bytes when several tainted words meet in one operation. The contract stores
// x0 = SLOAD(0) at memory 0x00 and x1 = SLOAD(1) at 0x20, hashes [0x00, 0x40) (one guard per tainted word),
// loads the word at 0x10 (composed from both), and writes both results; then SSTORE(0, x0 + 1). Every
// endorser must sign identical bytes, so 30 extractions of the same execution must encode identically.

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRProgramDeterministicMemory(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	code := []byte{
		0x60, 0x00, 0x54, 0x60, 0x00, 0x52, // MSTORE(0x00, SLOAD(0))
		0x60, 0x01, 0x54, 0x60, 0x20, 0x52, // MSTORE(0x20, SLOAD(1))
		0x60, 0x40, 0x60, 0x00, 0x20, // KECCAK256(0x00, 0x40)
		0x60, 0x01, 0x90, 0x55, // SSTORE(hash, 1)
		0x60, 0x10, 0x51, 0x60, 0x02, 0x55, // SSTORE(2, MLOAD(0x10))
		0x60, 0x00, 0x54, 0x60, 0x01, 0x01, 0x60, 0x00, 0x55, // SSTORE(0, SLOAD(0) + 1)
		0x00,
	}
	c := eprGasDeploy(t, e, owner, code)
	pow := func(n uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), n) }
	e.setKey(storeKey(c, common.Hash{}), new(big.Int).Add(pow(200), big.NewInt(12345)))
	e.setKey(storeKey(c, common.BigToHash(big.NewInt(1))), new(big.Int).Add(pow(180), big.NewInt(678)))
	var first []byte
	for i := 0; i < 30; i++ {
		snap := e.sdb.Snapshot()
		tr := NewEPRProgTracer()
		if _, _, err := e.evm(user, tr.Hooks()).Call(user, c, nil, vm.NewGasBudget(1_000_000), new(uint256.Int)); err != nil {
			t.Fatal(err)
		}
		e.sdb.RevertToSnapshot(snap)
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		p := tr.Program(writes)
		if p == nil {
			t.Fatalf("no program (lostAt=%q)", tr.lostAt)
		}
		enc := p.Encode()
		if first == nil {
			first = enc
			t.Logf("program: nodes=%d guards=%d bytes=%d", len(p.Nodes), len(p.Guards), len(enc))
			continue
		}
		if !bytes.Equal(enc, first) {
			t.Fatalf("extraction %d encodes differently from the first (%d vs %d bytes)", i, len(enc), len(first))
		}
	}
}
