// d16coverage measures how much of real Ethereum mainnet traffic the EPR extractor (the same code the
// fabric-x-evm endorser runs: EPRTracer + EPRProgTracer + EPRRewriteTx) turns into repairable writes.
//
// For every transaction of the sampled blocks it fetches the pre-state of the accounts and slots the
// transaction touches (debug_traceTransaction with prestateTracer), replays the transaction in a fresh
// in-memory go-ethereum state with the EPR tracers attached, checks the replay against the receipt
// (status and gas), builds the endorser's read-write set ("str:<addr>:<slot>" keys, every pre-existing
// slot read at a ledger version) and classifies every storage write as a guarded delta, part of a signed
// program, or plain. Two settings: "strict" keeps the gas headroom rule (assumption A7: 20k gas left per
// written slot, with the transaction's real gas limit, which mainnet wallets set for plain execution);
// "capable" assumes the client leaves enough headroom.
//
// Output: one JSON line per transaction. Usage:
//
//	d16coverage -rpc https://eth.drpc.org -start 24000000 -runs 20 -run-len 10 -stride 50000 -out cov.jsonl
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/hyperledger/fabric-x-sdk/blocks"

	"github.com/hyperledger/fabric-x-evm/endorser/execution"
)

var rpcURL string

// cacheDir, if set, keeps every successful RPC result (gzip, keyed by the request), so that a rerun with
// another extractor version replays exactly the same inputs without the network.
var cacheDir string

func cachePath(body []byte) string {
	h := sha256.Sum256(body)
	return filepath.Join(cacheDir, hex.EncodeToString(h[:16])+".json.gz")
}

func cacheGet(body []byte) ([]byte, bool) {
	if cacheDir == "" {
		return nil, false
	}
	f, err := os.Open(cachePath(body))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, false
	}
	b, err := io.ReadAll(zr)
	return b, err == nil
}

func cachePut(body, result []byte) {
	if cacheDir == "" {
		return
	}
	p := cachePath(body)
	tmp, err := os.CreateTemp(cacheDir, ".tmp-*")
	if err != nil {
		return
	}
	zw := gzip.NewWriter(tmp)
	_, err = zw.Write(result)
	if err2 := zw.Close(); err == nil {
		err = err2
	}
	if err2 := tmp.Close(); err == nil {
		err = err2
	}
	if err != nil {
		os.Remove(tmp.Name())
		return
	}
	os.Rename(tmp.Name(), p)
}

func call(method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if b, ok := cacheGet(body); ok {
		return json.Unmarshal(b, out)
	}
	var last error
	for i := 0; i < 6; i++ {
		resp, err := http.Post(rpcURL, "application/json", bytes.NewReader(body))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var r struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err = json.Unmarshal(b, &r); err == nil {
				if r.Error != nil {
					err = fmt.Errorf("rpc: %s", r.Error.Message)
				} else if len(r.Result) == 0 || string(r.Result) == "null" {
					err = fmt.Errorf("rpc: empty result")
				} else {
					cachePut(body, r.Result)
					return json.Unmarshal(r.Result, out)
				}
			}
		}
		last = err
		time.Sleep(time.Duration(1+2*i) * time.Second)
	}
	return last
}

type account struct {
	Balance *hexutil.Big                `json:"balance"`
	Nonce   uint64                      `json:"nonce"`
	Code    hexutil.Bytes               `json:"code"`
	Storage map[common.Hash]common.Hash `json:"storage"`
}

// rpcBlock decodes the header and the transactions separately: types.Header has its own UnmarshalJSON,
// which would take over the whole object if the header were embedded.
type rpcBlock struct {
	Header types.Header
	Txs    []*types.Transaction
}

func (b *rpcBlock) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &b.Header); err != nil {
		return err
	}
	var t struct {
		Txs []*types.Transaction `json:"transactions"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	b.Txs = t.Txs
	return nil
}

type receipt struct {
	Status  hexutil.Uint64 `json:"status"`
	GasUsed hexutil.Uint64 `json:"gasUsed"`
}

type chainCtx struct{}

func (chainCtx) Engine() consensus.Engine                    { return nil }
func (chainCtx) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (chainCtx) Config() *params.ChainConfig                 { return params.MainnetChainConfig }
func (chainCtx) CurrentHeader() *types.Header                { return nil }
func (chainCtx) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (chainCtx) GetHeaderByHash(common.Hash) *types.Header   { return nil }

// storeKey is the endorser's storage key (endorser/execution/statedb.go).
func storeKey(addr common.Address, slot common.Hash) string {
	return "str:" + addr.Hex() + ":" + slot.Hex()
}

type writeOut struct {
	Key    string `json:"k"`
	Strict string `json:"s"` // delta | program | plain
	Cap    string `json:"c"`
}

type txOut struct {
	WhyStrict map[string]int64 `json:"why_s,omitempty"` // plain-write reasons of the delta extractor
	WhyCap    map[string]int64 `json:"why_c,omitempty"`
	ProgLost  string           `json:"prog_lost,omitempty"` // where program extraction gave up (capable run)
	Block     uint64           `json:"block"`
	Index     int              `json:"i"`
	Hash      string           `json:"hash"`
	To        string           `json:"to"`
	Status    string           `json:"status"` // ok | replay_mismatch | error:<...>
	GasLim    uint64           `json:"gas_limit"`
	GasUsed   uint64           `json:"gas_used"`
	Writes    []writeOut       `json:"writes"`
}

func classify(v []byte) string {
	if len(v) == 30 && bytes.HasPrefix(v, execution.EPRMagic2) {
		return "delta"
	}
	if _, ok := execution.DecodeEPRProgram(v); ok {
		return "program"
	}
	return "plain"
}

// replay runs one transaction on its pre-state and returns the read-write set and the tracers.
// capableExtra is the gas added to the limit for the capable setting: a client of an EPR chain leaves
// room for commit-time evaluation, and the per-opcode gas rule is then checked on that run, so callees with a
// fixed allowance still count against it.
const capableExtra = 10_000_000

func replay(hdr *types.Header, tx *types.Transaction, pre map[common.Address]*account, extra uint64) (blocks.ReadWriteSet, *execution.EPRTracer, *execution.EPRProgTracer, *core.ExecutionResult, error) {
	sdb, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	if err != nil {
		return blocks.ReadWriteSet{}, nil, nil, nil, err
	}
	for a, acc := range pre {
		if acc.Balance != nil {
			b, _ := uint256.FromBig((*big.Int)(acc.Balance))
			sdb.SetBalance(a, b, tracing.BalanceChangeUnspecified)
		}
		sdb.SetNonce(a, acc.Nonce, tracing.NonceChangeUnspecified)
		if len(acc.Code) > 0 {
			sdb.SetCode(a, acc.Code, tracing.CodeChangeUnspecified)
		}
		for k, v := range acc.Storage {
			sdb.SetState(a, k, v)
		}
	}
	sdb.Finalise(true)
	cfg := params.MainnetChainConfig
	msg, err := core.TransactionToMessage(tx, types.MakeSigner(cfg, hdr.Number, hdr.Time), hdr.BaseFee)
	if err != nil {
		return blocks.ReadWriteSet{}, nil, nil, nil, err
	}
	if extra > 0 {
		// stay within the per-transaction gas cap of EIP-7825 (2^24), which the replayed blocks enforce
		if msg.GasLimit >= 1<<24 {
			extra = 0
		} else if msg.GasLimit+extra > 1<<24 {
			extra = 1<<24 - msg.GasLimit
		}
		msg.GasLimit += extra
		price := msg.GasPrice
		if msg.GasFeeCap != nil && (price == nil || msg.GasFeeCap.Cmp(price) > 0) {
			price = msg.GasFeeCap
		}
		if price != nil && price.Sign() > 0 {
			top := new(uint256.Int).Mul(uint256.NewInt(extra), price)
			sdb.AddBalance(msg.From, top, tracing.BalanceChangeUnspecified)
		}
	}
	d, p := execution.NewEPRTracer(), execution.NewEPRProgTracer()
	bctx := core.NewEVMBlockContext(hdr, chainCtx{}, &hdr.Coinbase)
	evm := vm.NewEVM(bctx, sdb, cfg, vm.Config{Tracer: execution.EPRCombineHooks(d, p)})
	gp := core.NewGasPool(hdr.GasLimit + extra)
	res, err := core.ApplyMessage(evm, msg, gp)
	if err != nil {
		return blocks.ReadWriteSet{}, nil, nil, nil, err
	}
	var rws blocks.ReadWriteSet
	ver := &blocks.Version{BlockNum: hdr.Number.Uint64() - 1}
	for a, acc := range pre {
		for k, v := range acc.Storage {
			key := storeKey(a, k)
			rws.Reads = append(rws.Reads, blocks.KVRead{Key: key, Version: ver})
			if nv := sdb.GetState(a, k); nv != v {
				rws.Writes = append(rws.Writes, blocks.KVWrite{Key: key, Value: nv.Bytes()})
			}
		}
	}
	return rws, d, p, res, nil
}

func main() {
	var start, stride uint64
	var runs, runLen int
	var out string
	flag.StringVar(&rpcURL, "rpc", "https://eth.drpc.org", "Ethereum JSON-RPC endpoint with debug_traceTransaction")
	flag.Uint64Var(&start, "start", 24000000, "first block of the first run")
	flag.IntVar(&runs, "runs", 20, "number of runs of consecutive blocks")
	flag.IntVar(&runLen, "run-len", 10, "consecutive blocks per run (hotness across blocks)")
	flag.Uint64Var(&stride, "stride", 50000, "blocks between run starts")
	flag.StringVar(&out, "out", "coverage.jsonl", "output (appended; blocks already present are skipped)")
	flag.StringVar(&cacheDir, "cache", "", "directory caching RPC results (reruns replay the same inputs offline)")
	flag.Parse()
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			log.Fatal(err)
		}
	}
	execution.EPREnabled, execution.EPRProgEnabled = true, true

	done := map[uint64]bool{}
	if f, err := os.Open(out); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var t txOut
			if json.Unmarshal(sc.Bytes(), &t) == nil {
				done[t.Block] = true
			}
		}
		f.Close()
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for r := 0; r < runs; r++ {
		for j := 0; j < runLen; j++ {
			bn := start + uint64(r)*stride + uint64(j)
			if done[bn] {
				continue
			}
			var blk rpcBlock
			if err := call("eth_getBlockByNumber", []any{hexutil.EncodeUint64(bn), true}, &blk); err != nil {
				log.Printf("block %d: %v", bn, err)
				continue
			}
			var rcpts []receipt
			if err := call("eth_getBlockReceipts", []any{hexutil.EncodeUint64(bn)}, &rcpts); err != nil || len(rcpts) != len(blk.Txs) {
				log.Printf("block %d receipts: %v", bn, err)
				continue
			}
			hdr := &blk.Header
			nOK := 0
			for i, tx := range blk.Txs {
				o := txOut{Block: bn, Index: i, Hash: tx.Hash().Hex(), GasLim: tx.Gas()}
				if tx.To() != nil {
					o.To = strings.ToLower(tx.To().Hex())
				}
				var pre map[common.Address]*account
				if err := call("debug_traceTransaction", []any{tx.Hash().Hex(), map[string]any{"tracer": "prestateTracer"}}, &pre); err != nil {
					o.Status = "error:prestate"
					_ = enc.Encode(o)
					continue
				}
				// each setting replays with fresh tracers: a rewrite consumes the tracers' state
				rws, d, p, res, err := replay(hdr, tx, pre, 0)
				if err != nil {
					o.Status = "error:" + strings.SplitN(err.Error(), ":", 2)[0]
					_ = enc.Encode(o)
					continue
				}
				o.GasUsed = res.UsedGas
				if res.Failed() != (rcpts[i].Status == 0) || res.UsedGas != uint64(rcpts[i].GasUsed) {
					o.Status = "replay_mismatch"
					_ = enc.Encode(o)
					continue
				}
				o.Status = "ok"
				// strict: real headroom (gas limit minus gas used before refunds)
				headroom := tx.Gas() - res.MaxUsedGas
				d.GasLeft = headroom
				r0 := execution.EPRPlainReasons()
				strict := execution.EPRRewriteTx(copyRWS(rws), d, p)
				o.WhyStrict = reasonDiff(r0, execution.EPRPlainReasons())
				// capable: ample headroom (a client of an EPR chain leaves room for commit-time evaluation)
				rws2, d2, p2, _, err := replay(hdr, tx, pre, capableExtra)
				if err != nil {
					o.Status = "error:replay2"
					_ = enc.Encode(o)
					continue
				}
				d2.GasLeft = 1 << 40
				r1 := execution.EPRPlainReasons()
				capable := execution.EPRRewriteTx(rws2, d2, p2)
				o.WhyCap = reasonDiff(r1, execution.EPRPlainReasons())
				if l := p2.LostAt(); l != "" {
					o.ProgLost = l
				}
				sv, cv := map[string]string{}, map[string]string{}
				for _, w := range strict.Writes {
					sv[w.Key] = classify(w.Value)
				}
				for _, w := range capable.Writes {
					cv[w.Key] = classify(w.Value)
				}
				for _, w := range rws.Writes {
					o.Writes = append(o.Writes, writeOut{Key: w.Key, Strict: sv[w.Key], Cap: cv[w.Key]})
				}
				_ = enc.Encode(o)
				nOK++
			}
			log.Printf("block %d: %d txs, %d replayed ok", bn, len(blk.Txs), nOK)
		}
	}
}

func reasonDiff(a, b map[string]int64) map[string]int64 {
	var out map[string]int64
	for k, v := range b {
		if d := v - a[k]; d > 0 {
			if out == nil {
				out = map[string]int64{}
			}
			out[k] = d
		}
	}
	return out
}

func copyRWS(r blocks.ReadWriteSet) blocks.ReadWriteSet {
	return blocks.ReadWriteSet{
		Reads:  append([]blocks.KVRead(nil), r.Reads...),
		Writes: append([]blocks.KVWrite(nil), r.Writes...),
	}
}
