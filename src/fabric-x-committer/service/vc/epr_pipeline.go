package vc

// Extension (not upstream): EPR's per-batch work in its own pipeline stage. The validator stage runs one worker
// (max-workers-for-validator: 1), and with EPR that worker did two things per batch: the version check, which
// every committer does, and EPR's own work on the batch (repair of stale signed reads, credits, programs, guards),
// which only EPR adds. With FX_EPR_PIPE (default on with EPR), the worker hands the checked batch to a second
// goroutine and starts the next batch's version check at once; the second goroutine does EPR's work, invalidates
// the remaining conflicts and passes the batch on to the committer, in batch order.
// Serial equivalence is unaffected: the dependency graph releases a transaction only after every conflicting
// predecessor has committed, so a batch never depends on the batch whose repair overlaps its version check (the
// same argument that already lets the validator run ahead of the committer, and the value prefetch run alongside
// the version check); credits commute and are range-checked as before.
// The re-execution baselines (FX_REEXEC_URL) and the in-block reorderings (WARD, Fabric++) keep the single-stage
// loop.

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/hyperledger/fabric-x-committer/utils/channel"
	"github.com/hyperledger/fabric-x-committer/utils/monitoring/promutil"
)

var (
	eprPipeOn = eprEnabled && os.Getenv("FX_EPR_PIPE") != "0" && os.Getenv("FX_REEXEC_URL") == "" &&
		os.Getenv("FX_WARD") != "1" && os.Getenv("FX_REORDER") == "" && os.Getenv("FX_FABRICSHARP") != "1"
	// time the validator worker waited for the EPR stage to take a batch
	eprVTHandoff atomic.Int64
)

const eprPipeDepth = 4

type eprChecked struct {
	prep      *preparedTransactions
	conflicts namespaceToReads
	pre       *eprPrefetched
	start     time.Time
}

func (v *transactionValidator) eprValidatePipelined(ctx context.Context, db *database) error {
	g, gCtx := errgroup.WithContext(ctx)
	handoff := make(chan eprChecked, eprPipeDepth)
	g.Go(func() error { // the validator worker: version check (and the concurrent value prefetch)
		defer close(handoff)
		incoming := channel.NewReader(gCtx, v.incomingPreparedTransactions)
		for {
			prepTx, ok := incoming.Read()
			if !ok {
				return nil
			}
			prepTx.Debug()
			start := time.Now()
			conflicts, pre, err := v.eprValidateReads(gCtx, db, prepTx)
			if err != nil {
				return err
			}
			tHand := time.Now()
			select {
			case handoff <- eprChecked{prep: prepTx, conflicts: conflicts, pre: pre, start: start}:
			case <-gCtx.Done():
				return nil
			}
			eprVTHandoff.Add(int64(time.Since(tHand)))
		}
	})
	g.Go(func() error { // the EPR stage: repair, invalidation, hand-over to the committer
		outgoing := channel.NewWriter(gCtx, v.outgoingValidatedTransactions)
		for b := range handoff {
			vTxs := &validatedTransactions{
				validTxNonBlindWrites: b.prep.txIDToNsNonBlindWrites,
				validTxBlindWrites:    b.prep.txIDToNsBlindWrites,
				newWrites:             b.prep.txIDToNsNewWrites,
				readToTxIDs:           b.prep.readToTxIDs,
				invalidTxStatus:       b.prep.invalidTxIDStatus,
				txIDToHeight:          b.prep.txIDToHeight,
			}
			pre := b.pre
			vTxs.eprPreWait = func() *eprPrefetched { return pre }
			conflicts, err := vTxs.pilotRepairDeltas(gCtx, db, b.conflicts)
			if err != nil {
				return err
			}
			if err := vTxs.invalidateTxsOnReadConflicts(conflicts); err != nil {
				return err
			}
			promutil.Observe(v.metrics.validatorTxBatchLatencySeconds, time.Since(b.start))
			vTxs.Debug()
			// After this point, vTxs is owned by the committer - DO NOT ACCESS.
			outgoing.Write(vTxs)
		}
		return nil
	})
	return g.Wait()
}
