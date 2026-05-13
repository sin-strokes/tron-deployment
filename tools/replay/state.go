package main

import (
	"encoding/json"
	"os"
)

// ReplayState 记录我们从主网拉到了哪一块。
//
// 重要：LastMainnetBlock 是**主网块号**，不是私链最高块号。
// 私链自己也在出块，私链 head 是私链链上的计数，与主网块号脱钩；
// 唯一可靠的"下次从哪抓"信息只有这里。
type ReplayState struct {
	LastMainnetBlock   int64 `json:"last_mainnet_block"`
	TotalFetched       int64 `json:"total_fetched"`
	TotalBroadcastOk   int64 `json:"total_broadcast_ok"`
	TotalBroadcastFail int64 `json:"total_broadcast_fail"`
	TotalSkipped       int64 `json:"total_skipped"`
}

// loadState 读取 state 文件；不存在 / 解析失败均返回零值。
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
