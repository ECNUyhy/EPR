package main

// Serial-equivalence check: with FX_ORDER_LOG=<file>, the perf harness records, for every EVM
// transaction in every delivered block, its ledger position, its validity and its raw bytes. d16oracle
// replays the valid ones serially in ledger order and compares the resulting state with the committer's
// database.

import (
	"encoding/hex"
	"fmt"
	"os"
	"sync"
)

var (
	eprOrderOnce sync.Once
	eprOrderFile *os.File
)

func eprOrderLog(block uint64, txNum int64, valid bool, raw []byte) {
	eprOrderOnce.Do(func() {
		if p := os.Getenv("FX_ORDER_LOG"); p != "" {
			eprOrderFile, _ = os.Create(p)
		}
	})
	if eprOrderFile == nil {
		return
	}
	v := 0
	if valid {
		v = 1
	}
	_, _ = fmt.Fprintf(eprOrderFile, "%d %d %d %s\n", block, txNum, v, hex.EncodeToString(raw))
}
