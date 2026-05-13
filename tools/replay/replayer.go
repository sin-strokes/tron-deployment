package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

const (
	// mainnetBlockSec is the mainnet block interval. Each mainnet block
	// represents 3 seconds of mainnet time.
	mainnetBlockSec = 3

	// tpsMultiplierDefault: default private TPS is ~1/3 of mainnet TPS.
	// This corresponds to a 9-second pace (one mainnet block spread over
	// 9 s of private chain time) and matches the 3 SR private vs 27 SR
	// mainnet topology (each private SR carries ~9x the mainnet density).
	// 0.333 is preferred over 1.0/3.0 for a cleaner --help output;
	// 9.009 s vs 9 s makes no practical difference.
	tpsMultiplierDefault = 0.333
)

// Replayer is the state machine driving the main replay loop:
//   - pull mainnet block → filter → broadcast to private chain with pacing
//     → advance state
//   - failed / skipped txs go to JSONL logs
//   - Ctrl+C triggers a graceful exit with state flushed for resume
type Replayer struct {
	cfg       Config
	trongrid  *TronGridClient
	private   *PrivateNodeClient
	state     ReplayState
	skipTypes map[string]struct{}
	failLog   *jsonlLogger
	skipLog   *jsonlLogger
}

// defaultEndOffset is how many blocks to advance when --end is not set.
// We deliberately avoid defaulting to "current mainnet head" so that
// running the tool without --end can't accidentally turn into a multi-hour
// job. Pass --end explicitly for longer ranges.
const defaultEndOffset = 10

func (r *Replayer) Run(ctx context.Context) error {
	start, err := r.resolveStartBlock(ctx)
	if err != nil {
		return fmt.Errorf("resolve start block: %w", err)
	}
	end := r.cfg.End
	if end <= 0 {
		end = start + defaultEndOffset
		log.Printf("--end not set, defaulting to start + %d = %d", defaultEndOffset, end)
	}
	log.Printf("Replay range: [%d, %d], skip types: %v", start, end, keysOf(r.skipTypes))

	for n := start; n <= end; n++ {
		select {
		case <-ctx.Done():
			log.Printf("ctx cancelled, exit at block %d", n)
			return r.flushState()
		default:
		}
		blockOk, blockFail, blockSkip, err := r.processBlock(ctx, n)
		if err != nil {
			// Don't advance LastMainnetBlock, but flush the cumulative
			// counters so they're visible for post-mortem.
			if flushErr := r.flushState(); flushErr != nil {
				log.Printf("save state failed: %v", flushErr)
			}
			log.Printf("ABORTING at block %d: %v", n, err)
			log.Printf("state preserved at last_mainnet_block=%d (not advanced to %d); "+
				"check %s for failure details", r.state.LastMainnetBlock, n, r.cfg.FailLog)
			return err
		}
		r.state.LastMainnetBlock = n
		if err := r.flushState(); err != nil {
			log.Printf("save state failed: %v", err)
		}
		log.Printf("block %d done | block: ok=%d fail=%d skip=%d | cum: ok=%d fail=%d skip=%d",
			n, blockOk, blockFail, blockSkip,
			r.state.TotalBroadcastOk, r.state.TotalBroadcastFail, r.state.TotalSkipped)
	}
	if err := r.flushState(); err != nil {
		return err
	}
	log.Printf("DONE | last=%d fetched=%d ok=%d fail=%d skip=%d",
		r.state.LastMainnetBlock, r.state.TotalFetched,
		r.state.TotalBroadcastOk, r.state.TotalBroadcastFail, r.state.TotalSkipped)
	return nil
}

// resolveStartBlock decides which **mainnet** block to start pulling from.
//
// Priority:
//  1. --start explicitly passed by the user
//  2. last_mainnet_block from the state file + 1
//
// IMPORTANT: never use the private chain's getnowblock + 1 as the start.
// The private chain produces its own blocks, so its head is the private
// chain's own counter — unrelated to mainnet's block number. On first
// run the user MUST pass --start explicitly (typically the snapshot's
// last mainnet block + 1).
func (r *Replayer) resolveStartBlock(_ context.Context) (int64, error) {
	if r.cfg.Start > 0 {
		return r.cfg.Start, nil
	}
	if r.state.LastMainnetBlock > 0 {
		return r.state.LastMainnetBlock + 1, nil
	}
	return 0, errors.New(
		"no start block: pass --start <mainnet_block_after_snapshot> on first run " +
			"(or run with an existing state file from a previous run)")
}

// processBlock handles a single mainnet block: fetch → split into per-second
// slots → broadcast to the private chain.
//
// Pacing algorithm:
//   - paceTotal = mainnetBlockSec / TpsMultiplier
//     e.g. multiplier=1   → pace=3s
//     multiplier=0.5 → pace=6s
//     multiplier=1/3 → pace=9s
//   - slots = max(1, floor(paceTotal / 1s)), ~1 second per slot
//     e.g. pace=3s → 3 slots, pace=9s → 9 slots, pace=1.5s → 1 slot
//   - n txs are spread across slots as evenly as possible: the first
//     (n % slots) slots get one extra (ceil(n/slots)) tx.
//   - Within a slot, txs are sent back-to-back with no sleep; after the
//     batch we wait until the slot's absolute deadline.
//   - Total timer invocations: `slots` (typically 3-9), not `n` (typically 150).
//
// On fetch failure we still wait for the full paceTotal to keep the
// schedule steady. Empty blocks / non-existent blocks return immediately
// without burning the full pace so replay advances faster.
//
// Returns (blockOk, blockFail, blockSkip, err).
// err is non-nil iff this block attempted broadcasts (attempted > 0) and
// all of them failed (ok == 0). In that case the caller (Run) should
// stop the service to avoid blindly advancing state while the private
// chain is in a bad state.
func (r *Replayer) processBlock(ctx context.Context, num int64) (int64, int64, int64, error) {
	blockStart := time.Now()

	okBefore := atomic.LoadInt64(&r.state.TotalBroadcastOk)
	failBefore := atomic.LoadInt64(&r.state.TotalBroadcastFail)
	skipBefore := atomic.LoadInt64(&r.state.TotalSkipped)

	multiplier := r.cfg.TpsMultiplier
	if multiplier <= 0 {
		multiplier = tpsMultiplierDefault
	}
	paceTotal := time.Duration(float64(mainnetBlockSec*time.Second) / multiplier)

	blk, err := r.trongrid.getBlock(ctx, num)
	if err != nil {
		log.Printf("block %d fetch failed: %v", num, err)
		r.waitUntil(ctx, blockStart.Add(paceTotal))
		return 0, 0, 0, nil // fetch failure is a TronGrid-side issue; not "all-fail", continue
	}
	if blk == nil {
		log.Printf("block %d not found", num)
		return 0, 0, 0, nil
	}
	n := len(blk.Transactions)
	atomic.AddInt64(&r.state.TotalFetched, int64(n))

	if n == 0 {
		return 0, 0, 0, nil
	}

	// Slot count ~= paceTotal in integer seconds. slotDuration = paceTotal/slots
	// absorbs any fractional remainder so the final slot's deadline lands
	// exactly at blockStart + paceTotal.
	slots := int(paceTotal / time.Second)
	if slots < 1 {
		slots = 1
	}
	slotDuration := paceTotal / time.Duration(slots)
	baseSize := n / slots
	remainder := n % slots // the first `remainder` slots each get one extra tx

	idx := 0
	for s := 0; s < slots; s++ {
		size := baseSize
		if s < remainder {
			size++
		}
		// Send this slot's `size` txs back-to-back, no sleep between.
		for k := 0; k < size; k++ {
			select {
			case <-ctx.Done():
				return atomic.LoadInt64(&r.state.TotalBroadcastOk) - okBefore,
					atomic.LoadInt64(&r.state.TotalBroadcastFail) - failBefore,
					atomic.LoadInt64(&r.state.TotalSkipped) - skipBefore,
					nil
			default:
			}
			r.processTx(ctx, num, blk.Transactions[idx])
			idx++
		}
		// Wait until the slot's absolute boundary (avoids drift).
		target := blockStart.Add(slotDuration * time.Duration(s+1))
		r.waitUntil(ctx, target)
	}

	blockOk := atomic.LoadInt64(&r.state.TotalBroadcastOk) - okBefore
	blockFail := atomic.LoadInt64(&r.state.TotalBroadcastFail) - failBefore
	blockSkip := atomic.LoadInt64(&r.state.TotalSkipped) - skipBefore

	// If we attempted broadcasts (blockFail > 0) and all of them failed
	// (blockOk == 0), trigger a shutdown. Skipped txs (VoteWitness etc.)
	// are excluded because we never attempted to broadcast them.
	if ctx.Err() == nil && blockFail > 0 && blockOk == 0 {
		return blockOk, blockFail, blockSkip,
			fmt.Errorf("block %d: all %d broadcast attempts failed", num, blockFail)
	}
	return blockOk, blockFail, blockSkip, nil
}

// processTx handles a single transaction: filter first; if it passes
// the filter, broadcast it.
func (r *Replayer) processTx(ctx context.Context, blockNum int64, tx json.RawMessage) {
	reason, peek := shouldSkip(tx, r.skipTypes)
	if reason != "" {
		atomic.AddInt64(&r.state.TotalSkipped, 1)
		r.skipLog.writeRecord(map[string]any{
			"block": blockNum, "txID": peek.TxID, "reason": reason,
		})
		return
	}
	ok, msg := r.private.broadcast(ctx, tx)
	if ok {
		atomic.AddInt64(&r.state.TotalBroadcastOk, 1)
	} else {
		atomic.AddInt64(&r.state.TotalBroadcastFail, 1)
		r.failLog.writeRecord(map[string]any{
			"block": blockNum, "txID": peek.TxID, "reason": msg,
		})
	}
}

// waitUntil blocks until `deadline` or until ctx is cancelled. Returns
// immediately if the deadline has already passed.
func (r *Replayer) waitUntil(ctx context.Context, deadline time.Time) {
	d := time.Until(deadline)
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (r *Replayer) flushState() error { return r.state.save(r.cfg.StateFile) }
