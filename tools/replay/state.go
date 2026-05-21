package main

import (
	"encoding/json"
	"os"
)

// ReplayState records which mainnet block we've pulled up to.
//
// IMPORTANT: LastMainnetBlock is the **mainnet block number**, not the
// private chain head. The private chain produces its own blocks; its
// head is the private chain's own counter and quickly decouples from
// the mainnet block number. The only reliable "where to resume from"
// information lives in this file.
//
// Per-tx checkpoint (InProgressBlock + InProgressTxIndex):
// covers SIGKILL / OOM / power-loss scenarios where graceful shutdown
// can't run. The tx index is flushed after every processed tx; on
// restart, a non-zero InProgressBlock tells us to resume from
// InProgressTxIndex within that block instead of re-broadcasting the
// whole block (which would otherwise flood replay-failures.jsonl
// with DUP_TRANSACTION_ERROR entries).
//
// When a block completes successfully, Run flushes a single atomic
// transition that advances LastMainnetBlock and clears the in-progress
// fields. If that final flush is itself killed, the next start re-enters
// processBlock with InProgressTxIndex >= the block's tx count, which is
// recognized and treated as already-completed.
type ReplayState struct {
	LastMainnetBlock   int64 `json:"last_mainnet_block"`
	InProgressBlock    int64 `json:"in_progress_block,omitempty"`
	InProgressTxIndex  int   `json:"in_progress_tx_index,omitempty"`
	TotalFetched       int64 `json:"total_fetched"`
	TotalBroadcastOk   int64 `json:"total_broadcast_ok"`
	TotalBroadcastFail int64 `json:"total_broadcast_fail"`
	TotalSkipped       int64 `json:"total_skipped"`
}

// loadState reads the state file; missing or unparseable files return
// a zero-valued ReplayState.
func loadState(path string) ReplayState {
	var s ReplayState
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	return s
}

func (s *ReplayState) save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
