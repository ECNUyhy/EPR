"""Derives the FabricSharp and Fabric++ packages for the Fabric-X coordinator from the original sources in
orig_src/ (see orig_src/README.md). Only the edits below are applied; each must match exactly once, so a
changed original makes the port fail instead of silently diverging.

FabricSharp (sigmod20 branch, sharpscheduler + common): algorithm code unchanged. Edits:
  - import paths of the two packages; the orderer config/flogging imports are dropped;
  - NewTxnScheduler takes max_span and the LevelDB path as arguments instead of reading the orderer's
    environment (TXN_SPAN_LIMIT, default 10; STORE_PATH);
  - the logger: per-transaction Info logs are dropped (an orderer logging at INFO writes several lines per
    transaction, which would charge FabricSharp for log I/O); warnings (DESERT) and errors are counted;
  - Fabric's order-preserving varint helpers (common/ledger/util) are copied into common/util.go.
Same library versions as the original (Gopkg.lock): willf/bloom v2.0.3, willf/bitset v1.1.8,
spaolacci/murmur3 v1.1.0, emirpasic/gods 1615341f (v1.12.0), syndtr/goleveldb.

Fabric++ (master branch, fppscheduler): resolver.go, johnsonce.go, tarjanscc.go unchanged. In
scheduler.go, ScheduleTxn receives the read keys with their versions and the write keys instead of parsing
a Fabric 2.2 chaincode action (the loops over them are unchanged), kvrwset.Version becomes a local struct
with the same fields, and the logger is replaced as above.

usage: python port_orig_baselines.py   (writes ported/)"""
import os
import re
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))
SRC, DST = os.path.join(HERE, "orig_src"), os.path.join(HERE, "ported", "fabricsharp")
MOD = "github.com/hyperledger/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp"


def edit(s, old, new, path):
    n = s.count(old)
    assert n == 1, f"{path}: expected one match of {old!r}, found {n}"
    return s.replace(old, new)


def port(rel_src, rel_dst, edits):
    src = os.path.join(SRC, rel_src)
    s = open(src, encoding="utf-8").read()
    for old, new in edits:
        s = edit(s, old, new, rel_src)
    dst = os.path.join(DST, rel_dst)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    hdr = f"// Ported from FabricSharp ({rel_src}); see port_orig_baselines.py for the edits.\n\n"
    open(dst, "w", encoding="utf-8", newline="\n").write(hdr + s)


if os.path.isdir(DST):
    shutil.rmtree(DST)

# ---- FabricSharp: sharpscheduler
port("sigmod20/sharpscheduler/scheduler.go", "sharpscheduler/scheduler.go", [
    ('\t"github.com/hyperledger/fabric/orderer/common/blockcutter/scheduler"\n', ""),
    ('"github.com/hyperledger/fabric/orderer/common/blockcutter/scheduler/common"', f'"{MOD}/common"'),
    ('\n\t"github.com/hyperledger/fabric/common/flogging"\n\t"github.com/hyperledger/fabric/orderer/common/localconfig"\n', ""),
    ('var logger = flogging.MustGetLogger("orderer/common/blockcutter/scheduler")', "var logger = common.Logger"),
    ("func NewTxnScheduler() scheduler.Scheduler {\n\tstorePath := localconfig.MustGetStoragePath()\n",
     "func NewTxnScheduler(maxTxnSpan uint64, storePath string) *TxnScheduler {\n"),
    ("return createTxnScheduler(localconfig.GetFSharpTxnSpanLimitWithDefault(), false, storePath)",
     "return createTxnScheduler(maxTxnSpan, false, storePath)"),
])
port("sigmod20/sharpscheduler/scheduler_test.go", "sharpscheduler/scheduler_test.go", [])
# ---- FabricSharp: common
port("sigmod20/common/ds.go", "common/ds.go", [])
port("sigmod20/common/ds_test.go", "common/ds_test.go", [])
port("sigmod20/common/mv_store.go", "common/mv_store.go", [
    ('\t"github.com/hyperledger/fabric/common/ledger/util"\n', ""),
    ("util.DecodeOrderPreservingVarUint64(", "decodeOrderPreservingVarUint64("),
    ("util.EncodeOrderPreservingVarUint64(", "encodeOrderPreservingVarUint64("),
])
port("sigmod20/common/mv_store_test.go", "common/mv_store_test.go", [])
open(os.path.join(DST, "common", "util.go"), "w", encoding="utf-8", newline="\n").write('''// Copied from Hyperledger Fabric common/ledger/util (Apache-2.0); proto varints of the size byte (at most 8)
// are written as that single byte, which is what proto.EncodeVarint produces for values below 128.

package common

import (
	"encoding/binary"
	"sync/atomic"
)

func encodeOrderPreservingVarUint64(number uint64) []byte {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, number)
	startingIndex := 0
	size := 0
	for i, b := range bytes {
		if b != 0x00 {
			startingIndex = i
			size = 8 - i
			break
		}
	}
	encodedBytes := make([]byte, size+1)
	encodedBytes[0] = byte(size)
	copy(encodedBytes[1:], bytes[startingIndex:])
	return encodedBytes
}

func decodeOrderPreservingVarUint64(bytes []byte) (uint64, int) {
	size := int(bytes[0])
	decodedBytes := make([]byte, 8)
	copy(decodedBytes[8-size:], bytes[1:size+1])
	numBytesConsumed := size + 1
	return binary.BigEndian.Uint64(decodedBytes), numBytesConsumed
}

// fsLogger replaces the orderer's flogging logger (see port_orig_baselines.py).
type fsLogger struct{}

// Logger is the logger of the ported packages.
var Logger fsLogger

// Warnings and Errors count the scheduler's warnings (DESERT: a false-positive cycle) and errors.
var Warnings, Errors atomic.Int64

func (fsLogger) Infof(string, ...interface{})  {}
func (fsLogger) Debugf(string, ...interface{}) {}
func (fsLogger) Warnf(string, ...interface{})  { Warnings.Add(1) }
func (fsLogger) Errorf(string, ...interface{}) { Errors.Add(1) }
func (fsLogger) Panicf(f string, a ...interface{}) {
	panic(f)
}
''')

# ---- Fabric++ (fppscheduler from FabricSharp master)
for f in ("resolver.go", "johnsonce.go", "tarjanscc.go"):
    port(f"master/fppscheduler/{f}", f"fppscheduler/{f}", [])
port("master/fppscheduler/scheduler.go", "fppscheduler/scheduler.go", [
    ('''import (
	"time"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/orderer/common/blockcutter/scheduler"
)

var logger = flogging.MustGetLogger("orderer.common.blockcutter.scheduler")
''', '''import (
	"time"

	"%s/common"
)

var logger = common.Logger

// Version replaces kvrwset.Version (same fields).
type Version struct {
	BlockNum uint64
	TxNum    uint64
}

// Read is a read key with the version read, as in a kvrwset.KVRead.
type Read struct {
	Key     string
	Version *Version
}
''' % MOD),
    ("keyVersionMap map[uint32]*kvrwset.Version", "keyVersionMap map[uint32]*Version"),
    ("func NewTxnScheduler(blkSize uint32) scheduler.Scheduler {", "func NewTxnScheduler(blkSize uint32) *TxnScheduler {"),
    ("\t\tkeyVersionMap: make(map[uint32]*kvrwset.Version),\n", "\t\tkeyVersionMap: make(map[uint32]*Version),\n"),
    ("\t\tscheduler.keyVersionMap = make(map[uint32]*kvrwset.Version)\n", "\t\tscheduler.keyVersionMap = make(map[uint32]*Version)\n"),
    ("func (scheduler *TxnScheduler) ScheduleTxn(resppayload *peer.ChaincodeAction, nextCommittedHeight uint64, txnID string) bool {",
     "func (scheduler *TxnScheduler) ScheduleTxn(reads []Read, writes []string, nextCommittedHeight uint64, txnID string) bool {"),
    ('''	// get current transaction id
	var err error
	txRWSet := &rwsetutil.TxRwSet{}
	if err = txRWSet.FromProtoBytes(resppayload.Results); err != nil {
		logger.Panicf("from proto bytes error")
	}
	tid''', "\ttid"),
    ("\n\tns := txRWSet.NsRwSets[0]\n\t// For some reason", "\n\t// For some reason"),
    ("\tfor _, write := range ns.KvRwSet.Writes {\n\t\twriteKey := write.GetKey()\n",
     "\tfor _, writeKey := range writes {\n"),
    ("\tfor _, read := range ns.KvRwSet.Reads {\n\t\treadKey := read.GetKey()\n\t\treadVer := read.GetVersion()\n",
     "\tfor _, read := range reads {\n\t\treadKey := read.Key\n\t\treadVer := read.Version\n"),
])
print("ported into", DST)
for root, _, files in os.walk(DST):
    for f in sorted(files):
        print(" ", os.path.relpath(os.path.join(root, f), DST))
