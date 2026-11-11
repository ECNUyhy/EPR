// Command d16reexec is the post-order re-execution service of the re-execution baselines
// (XOX / HTFabric / ReX style). It runs next to the validator-committer, inside the committer container,
// and re-executes conflicting transactions with fabric-x-evm's executor against the latest committed state
// in the committer's database: the VC sends the raw transactions of a batch in ledger order, the service
// runs them on the state left by their predecessors (sequentially, or in TSP partitions with -parallel),
// and returns the final value of every storage slot they wrote. As in a re-executing committer,
// re-execution runs on the commit path.
//
// usage: d16reexec -listen 127.0.0.1:7071 -db postgres://postgres:postgres@localhost:5433/postgres -chain 4011
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"hash/fnv"
	"log"
	"math/big"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/hyperledger/fabric-x-sdk/blocks"
	"github.com/yugabyte/pgx/v5"
	"github.com/yugabyte/pgx/v5/pgxpool"

	fxcommon "github.com/hyperledger/fabric-x-evm/common"
	"github.com/hyperledger/fabric-x-evm/endorser/execution"
)

// store reads the latest committed state from the committer database, overlaid with the writes of the
// transactions already re-executed in the current request.
type store struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	table    string
	overlay  map[string][]byte
	prefetch map[string][]byte // endorsed read keys fetched in one query per request (optimized mode)
}

// codeCache holds contract code, which never changes once deployed (optimized mode).
var codeCache sync.Map

func (s *store) Get(_ string, key string) (*blocks.WriteRecord, error) {
	if v, ok := s.overlay[key]; ok {
		return &blocks.WriteRecord{Key: key, Value: v, Version: 1}, nil
	}
	if v, ok := s.prefetch[key]; ok {
		if v == nil {
			return nil, nil
		}
		return &blocks.WriteRecord{Key: key, Value: v, Version: 1}, nil
	}
	isCode := *optimized && strings.HasSuffix(key, ":code")
	if isCode {
		if v, ok := codeCache.Load(key); ok {
			return &blocks.WriteRecord{Key: key, Value: v.([]byte), Version: 1}, nil
		}
	}
	var val []byte
	var ver int64
	err := s.pool.QueryRow(s.ctx, "SELECT value, version FROM "+s.table+" WHERE key = $1", []byte(key)).Scan(&val, &ver)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if isCode {
		codeCache.Store(key, val)
	}
	return &blocks.WriteRecord{Key: key, Value: val, Version: uint64(ver)}, nil //nolint:gosec
}

// prefetchKeys loads the given keys in one query (absent keys are recorded as nil).
func (s *store) prefetchKeys(keys []string) error {
	s.prefetch = make(map[string][]byte, len(keys))
	bk := make([][]byte, len(keys))
	for i, k := range keys {
		bk[i] = []byte(k)
		s.prefetch[k] = nil
	}
	rows, err := s.pool.Query(s.ctx, "SELECT key, value FROM "+s.table+" WHERE key = ANY($1)", bk)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		s.prefetch[string(k)] = v
	}
	return rows.Err()
}

var optimized = flag.Bool("optimized", true, "cache contract code and prefetch the endorsed read keys")

func (s *store) Close() error { return nil }

// parallel selects HTFabric's re-execution (Song et al., CIKM'24): the transactions are split into partitions
// by TSP (hashing each on its key of lowest degree in the batch, so that the hottest keys are the ones cut),
// and in every round one transaction per partition is re-executed in parallel on the state left by the
// previous rounds; the round's results are then applied in partition order, and a transaction whose read set
// was written by one applied before it in the same round is re-executed again, sequentially (its Algorithm 3).
var parallel = flag.Int("parallel", 0, "HTFabric mode: number of TSP partitions (0: sequential, XOX-style; -1: number of CPUs)")

type request struct {
	Txs    [][]byte   `json:"txs"`     // carrier values: 20-byte sender | raw signed transaction
	Keys   []string   `json:"keys"`    // endorsed read keys of the transactions (prefetched in optimized mode)
	TxKeys [][]string `json:"tx_keys"` // per transaction: its endorsed read keys (TSP partitioning)
}

// execResult is the outcome of executing one transaction on the store's current state.
type execResult struct {
	ok     bool
	reads  map[string]bool
	writes map[string][]byte // storage slots written
}

func execOne(ctx context.Context, st *store, ns string, cfg execution.EVMConfig, c []byte) (execResult, error) {
	res := execResult{reads: map[string]bool{}, writes: map[string][]byte{}}
	if len(c) < 21 {
		return res, errors.New("short carrier")
	}
	from := common.BytesToAddress(c[:20])
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(c[20:]); err != nil {
		return res, err
	}
	sdb, err := execution.NewStateDB(ctx, st, ns, true)
	if err != nil {
		return res, err
	}
	ex, err := execution.NewExecutor(sdb, st, big.NewInt(1), uint64(time.Now().Unix()), cfg) //nolint:gosec
	if err != nil {
		return res, err
	}
	if _, _, err := ex.Call(ethereum.CallMsg{From: from, To: tx.To(), Gas: tx.Gas(), Value: tx.Value(), Data: tx.Data()}); err != nil {
		return res, nil // the transaction aborts, as it would in serial execution
	}
	res.ok = true
	rws := sdb.Result()
	for _, rd := range rws.Reads {
		res.reads[rd.Key] = true
	}
	for _, wr := range rws.Writes {
		if strings.HasPrefix(wr.Key, "str:") {
			res.writes[wr.Key] = wr.Value
		}
	}
	return res, nil
}

// tspPartitions assigns every transaction to one of np partitions (HTFabric's TSP).
func tspPartitions(txKeys [][]string, n, np int) [][]int {
	deg := map[string]int{}
	for _, ks := range txKeys {
		for _, k := range ks {
			deg[k]++
		}
	}
	parts := make([][]int, np)
	for i := 0; i < n; i++ {
		p := i % np
		if i < len(txKeys) && len(txKeys[i]) > 0 {
			best := ""
			for _, k := range txKeys[i] {
				if best == "" || deg[k] < deg[best] || (deg[k] == deg[best] && k < best) {
					best = k
				}
			}
			h := fnv.New32a()
			_, _ = h.Write([]byte(best))
			p = int(h.Sum32() % uint32(np)) //nolint:gosec
		}
		parts[p] = append(parts[p], i)
	}
	return parts
}

type response struct {
	Final    map[string][]byte `json:"final"`
	OK       []bool            `json:"ok"` // per transaction (request order): re-execution succeeded
	// per transaction (request order): its storage writes, so that a committer can commit the re-executed
	// outcome of transactions it cannot repair (XOX and HTFabric re-execute every invalid transaction)
	TxWrites []map[string][]byte `json:"tx_writes"`
	Reverted int               `json:"reverted"`
	Micros   int64             `json:"micros"`
}

var nTx, nReq, nRev, nMicros, nConflictReexec atomic.Int64

func main() {
	listen := flag.String("listen", "127.0.0.1:7071", "listen address")
	dsn := flag.String("db", "postgres://postgres:postgres@localhost:5433/postgres", "committer database")
	ns := flag.String("ns", "basic", "namespace")
	chain := flag.Int64("chain", 4011, "chain id")
	flag.Parse()
	pool, err := pgxpool.New(context.Background(), *dsn)
	if err != nil {
		log.Fatal(err)
	}
	cfg := execution.EVMConfig{ChainConfig: fxcommon.BuildChainConfig(*chain)}
	table := "ns_" + *ns
	go func() {
		for range time.Tick(10 * time.Second) {
			if n := nTx.Load(); n > 0 {
				log.Printf("D16REEXEC requests=%d txs=%d reverted=%d conflict_reexec=%d avg_us_per_tx=%.1f", nReq.Load(), n, nRev.Load(), nConflictReexec.Load(), float64(nMicros.Load())/float64(n))
			}
		}
	}()
	http.HandleFunc("/reexec", func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start := time.Now()
		st := &store{ctx: r.Context(), pool: pool, table: table, overlay: map[string][]byte{}}
		if *optimized && len(req.Keys) > 0 {
			if err := st.prefetchKeys(req.Keys); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		resp := response{Final: map[string][]byte{}}
		if np := *parallel; np != 0 {
			if np < 0 {
				np = runtime.NumCPU()
			}
			resp.OK = make([]bool, len(req.Txs))
			resp.TxWrites = make([]map[string][]byte, len(req.Txs))
			parts := tspPartitions(req.TxKeys, len(req.Txs), np)
			apply := func(r execResult) {
				for k, v := range r.writes {
					st.overlay[k] = v
					resp.Final[k] = v
				}
			}
			for round := 0; ; round++ {
				var batch []int
				for _, p := range parts {
					if round < len(p) {
						batch = append(batch, p[round])
					}
				}
				if len(batch) == 0 {
					break
				}
				results := make([]execResult, len(batch))
				errs := make([]error, len(batch))
				var wg sync.WaitGroup
				for j, i := range batch { // the overlay is only read during the round
					wg.Add(1)
					go func(j, i int) {
						defer wg.Done()
						results[j], errs[j] = execOne(r.Context(), st, *ns, cfg, req.Txs[i])
					}(j, i)
				}
				wg.Wait()
				written := map[string]bool{}
				for j, i := range batch { // apply in partition order
					if errs[j] != nil {
						http.Error(w, errs[j].Error(), http.StatusInternalServerError)
						return
					}
					res := results[j]
					conflict := false
					for k := range res.reads {
						if written[k] {
							conflict = true
							break
						}
					}
					if conflict { // read a slot an earlier transaction of the round wrote: re-execute sequentially
						var err error
						if res, err = execOne(r.Context(), st, *ns, cfg, req.Txs[i]); err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}
						nConflictReexec.Add(1)
					}
					resp.OK[i] = res.ok
					if !res.ok {
						resp.Reverted++
						continue
					}
					resp.TxWrites[i] = res.writes
					apply(res)
					for k := range res.writes {
						written[k] = true
					}
				}
			}
			resp.Micros = time.Since(start).Microseconds()
			nReq.Add(1)
			nTx.Add(int64(len(req.Txs)))
			nRev.Add(int64(resp.Reverted))
			nMicros.Add(resp.Micros)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		for _, c := range req.Txs {
			if len(c) < 21 {
				http.Error(w, "short carrier", http.StatusBadRequest)
				return
			}
			from := common.BytesToAddress(c[:20])
			tx := new(types.Transaction)
			if err := tx.UnmarshalBinary(c[20:]); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sdb, err := execution.NewStateDB(r.Context(), st, *ns, true)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ex, err := execution.NewExecutor(sdb, st, big.NewInt(1), uint64(time.Now().Unix()), cfg) //nolint:gosec
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if _, _, err := ex.Call(ethereum.CallMsg{From: from, To: tx.To(), Gas: tx.Gas(), Value: tx.Value(), Data: tx.Data()}); err != nil {
				resp.Reverted++ // the transaction aborts, as it would in serial execution
				resp.OK = append(resp.OK, false)
				resp.TxWrites = append(resp.TxWrites, nil)
				continue
			}
			resp.OK = append(resp.OK, true)
			txw := map[string][]byte{}
			for _, wr := range sdb.Result().Writes {
				if strings.HasPrefix(wr.Key, "str:") {
					st.overlay[wr.Key] = wr.Value
					resp.Final[wr.Key] = wr.Value
					txw[wr.Key] = wr.Value
				}
			}
			resp.TxWrites = append(resp.TxWrites, txw)
		}
		resp.Micros = time.Since(start).Microseconds()
		nReq.Add(1)
		nTx.Add(int64(len(req.Txs)))
		nRev.Add(int64(resp.Reverted))
		nMicros.Add(resp.Micros)
		_ = json.NewEncoder(w).Encode(resp)
	})
	log.Printf("d16reexec listening on %s (table %s)", *listen, table)
	log.Fatal(http.ListenAndServe(*listen, nil))
}
