package execution

// Init code taken from poisoned memory bytes. A word that depends on a storage read is stored at offset 0 and
// then half overwritten, so its other 16 bytes are poisoned: they still depend on the read but are no longer
// shadowed. CREATE2 uses them as init code, so the new contract's address depends on the value read, and the
// transaction stores that address. A program would replay the endorsed address on every base, so the tracer
// must give up (site 95), as MLOAD does on poisoned bytes. Control: the same transaction with the whole word as
// init code yields a program whose equality guard on the word pins the address.

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRCreatePoison(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	code := func(poison bool) []byte {
		c := []byte{0x60, 0x00, 0x54, 0x60, 0x00, 0x52} // x = SLOAD(0); MSTORE(0, x)
		size := byte(0x20)
		if poison {
			c = append(c, 0x60, 0x00, 0x60, 0x10, 0x52) // MSTORE(16, 0): bytes 0-15 of x are poisoned
			size = 0x10
		}
		c = append(c, 0x60, 0x00, 0x60, size, 0x60, 0x00, 0x60, 0x00, 0xf5) // CREATE2(0, 0, size, salt 0)
		c = append(c, 0x60, 0x01, 0x55)                                     // SSTORE(1, address)
		c = append(c, 0x60, 0x00, 0x54, 0x60, 0x01, 0x01, 0x60, 0x00, 0x55) // SSTORE(0, SLOAD(0) + 1)
		return append(c, 0x00)
	}
	for _, tc := range []struct {
		name   string
		poison bool
	}{{"poisoned-init-code", true}, {"whole-word-init-code", false}} {
		t.Run(tc.name, func(t *testing.T) {
			c := eprGasDeploy(t, e, owner, code(tc.poison))
			snap := e.sdb.Snapshot()
			defer e.sdb.RevertToSnapshot(snap)
			e.setKey(storeKey(c, common.Hash{}), new(big.Int).Lsh(big.NewInt(1), 200)) // nonzero high bytes
			tr := NewEPRProgTracer()
			if _, _, err := e.evm(user, tr.Hooks()).Call(user, c, nil, vm.NewGasBudget(2_000_000), new(uint256.Int)); err != nil {
				t.Fatalf("endorsed run: %v", err)
			}
			writes := map[string]bool{}
			for s, w := range tr.written {
				if w {
					writes[s] = true
				}
			}
			p := tr.Program(writes)
			if tc.poison {
				if p != nil || !strings.Contains(tr.lostAt, "site=95") {
					t.Fatalf("init code from poisoned bytes accepted (program %v, lostAt=%q)", p != nil, tr.lostAt)
				}
				t.Logf("lost: %s", tr.lostAt)
				return
			}
			if p == nil {
				t.Fatalf("control: no program (lostAt=%q)", tr.lostAt)
			}
		})
	}
}
