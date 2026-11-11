package execution

// SmallBank on the EVM. For each SmallBank operation, report which encoding EPR chooses (guarded
// deltas, guarded program, or plain), and differentially check the extracted content: re-execute the
// transaction on perturbed balances and compare with the evaluation of the signed content.

import (
	"encoding/binary"
	"encoding/json"
	"math/big"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
	"github.com/hyperledger/fabric-x-sdk/blocks"
)

func TestEPRSmallBank(t *testing.T) {
	path := os.Getenv("D16_SMALLBANK_JSON")
	if path == "" {
		t.Skip("D16_SMALLBANK_JSON not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var art struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatal(err)
	}
	sb, _ := abi.JSON(strings.NewReader(string(art.ABI)))
	e := newEPRUniEnv(t)
	_, bank, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		t.Fatal(err)
	}
	initBal := new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil)
	if err := e.call(e.owner, bank, nil, sb, "init", big.NewInt(0), big.NewInt(16), initBal, initBal); err != nil {
		t.Fatal(err)
	}
	prev := EPRProgEnabled
	EPRProgEnabled = true
	defer func() { EPRProgEnabled = prev }()

	type op struct {
		name string
		args func(r *rand.Rand) []interface{}
	}
	amt := func(r *rand.Rand) *big.Int { return big.NewInt(1 + r.Int63n(1000)) }
	acct := func(r *rand.Rand) *big.Int { return big.NewInt(r.Int63n(8)) }
	two := func(r *rand.Rand) (*big.Int, *big.Int) {
		a := r.Int63n(8)
		return big.NewInt(a), big.NewInt((a + 1 + r.Int63n(7)) % 8)
	}
	ops := []op{
		{"depositChecking", func(r *rand.Rand) []interface{} { return []interface{}{acct(r), amt(r)} }},
		{"transactSavings", func(r *rand.Rand) []interface{} {
			v := amt(r)
			if r.Intn(2) == 0 {
				v.Neg(v)
			}
			return []interface{}{acct(r), v}
		}},
		{"sendPayment", func(r *rand.Rand) []interface{} { a, b := two(r); return []interface{}{a, b, amt(r)} }},
		{"writeCheck", func(r *rand.Rand) []interface{} { return []interface{}{acct(r), amt(r)} }},
		{"amalgamate", func(r *rand.Rand) []interface{} { a, b := two(r); return []interface{}{a, b} }},
		{"balance", func(r *rand.Rand) []interface{} { return []interface{}{acct(r)} }},
	}
	rng := rand.New(rand.NewSource(7))
	pick := func(base *big.Int) *big.Int {
		switch rng.Intn(4) {
		case 0:
			return big.NewInt(rng.Int63n(2000)) // small: insufficient funds, penalty branch
		case 1:
			return new(big.Int).Set(base)
		default:
			return new(big.Int).Add(initBal, big.NewInt(rng.Int63n(2_000_000_000)-1_000_000_000))
		}
	}
	total := 0
	for _, o := range ops {
		enc := map[string]int{}
		var agree, revertMatch, falseRefuse int
		for c := 0; c < 50; c++ {
			in, err := sb.Pack(o.name, o.args(rng)...)
			if err != nil {
				t.Fatal(err)
			}
			snap := e.sdb.Snapshot()
			d, p := NewEPRTracer(), NewEPRProgTracer()
			if _, _, err := e.evm(e.user, EPRCombineHooks(d, p)).Call(e.user, bank, in, vm.NewGasBudget(1_000_000), new(uint256.Int)); err != nil {
				t.Fatalf("%s case %d: endorsement reverted: %v", o.name, c, err)
			}
			d.GasLeft = 1_000_000
			var rws blocks.ReadWriteSet
			keys := make([]string, 0, len(d.slots))
			for s := range d.slots {
				keys = append(keys, s)
			}
			sort.Strings(keys)
			for _, s := range keys {
				rws.Reads = append(rws.Reads, blocks.KVRead{Key: s, Version: &blocks.Version{}})
				if d.slots[s].written {
					rws.Writes = append(rws.Writes, blocks.KVWrite{Key: s, Value: e.getKey(s).FillBytes(make([]byte, 32))})
				}
			}
			out := EPRRewriteTx(rws, d, p)
			e.sdb.RevertToSnapshot(snap)

			kind := "plain"
			var prog *EPRProgram
			deltas := map[string][]byte{}
			for _, w := range out.Writes {
				switch {
				case len(w.Value) == 30 && string(w.Value[:5]) == string(EPRMagic2):
					deltas[w.Key] = w.Value
					kind = "deltas"
				case len(w.Value) > 5 && string(w.Value[:5]) == "EPRP1":
					prog, _ = DecodeEPRProgram(w.Value)
					kind = "program"
				}
			}
			if len(out.Writes) == 0 {
				kind = "read-only"
			}
			enc[kind]++
			if kind != "deltas" && kind != "program" {
				continue
			}
			var inputs []string
			if prog != nil {
				inputs = prog.Keys
			} else {
				for k := range deltas {
					inputs = append(inputs, k)
				}
			}
			for r := 0; r < 40; r++ {
				s3 := e.sdb.Snapshot()
				vals := map[string]*big.Int{}
				for _, k := range inputs {
					v := pick(e.getKey(k))
					vals[k] = v
					e.setKey(k, v)
				}
				want := map[string]*big.Int{}
				ok := true
				if prog != nil {
					outs, pok := prog.Eval(func(k string) []byte { return vals[k].Bytes() })
					ok = pok
					for k, w := range outs {
						want[k] = new(big.Int).SetBytes(w)
					}
				} else {
					for k, v := range deltas {
						delta := int64(binary.BigEndian.Uint64(v[5:13]))
						lo := int64(binary.BigEndian.Uint64(v[13:21]))
						hi := int64(binary.BigEndian.Uint64(v[21:29]))
						b := vals[k]
						if !b.IsInt64() {
							ok = false
							continue
						}
						n := b.Int64() + delta
						if n < lo || n > hi || (delta > 0 && n < b.Int64()) {
							ok = false
						}
						want[k] = big.NewInt(n)
					}
				}
				_, _, err := e.evm(e.user, nil).Call(e.user, bank, in, vm.NewGasBudget(1_000_000), new(uint256.Int))
				switch {
				case err != nil && ok:
					t.Fatalf("%s case %d/%d: EVM reverted (%v) but guards hold", o.name, c, r, err)
				case err != nil:
					revertMatch++
				case ok:
					for k, w := range want {
						if e.getKey(k).Cmp(w) != 0 {
							t.Fatalf("%s case %d/%d: key %s EPR %s EVM %s", o.name, c, r, k, w, e.getKey(k))
						}
					}
					agree++
				default:
					falseRefuse++
				}
				e.sdb.RevertToSnapshot(s3)
			}
		}
		total += agree + revertMatch + falseRefuse
		t.Logf("SMALLBANK %-16s encodings=%v reexec=%d equal=%d revert_match=%d guards_fail_evm_ok=%d",
			o.name, enc, agree+revertMatch+falseRefuse, agree, revertMatch, falseRefuse)
	}
	t.Logf("SMALLBANK total re-executions=%d", total)
}
