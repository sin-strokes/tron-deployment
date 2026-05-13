package main

import "encoding/json"

// defaultSkipContractTypes 列出默认跳过的合约类型。
//
// 这几类在重放路径上注定失败（参考 0.5.3 节）：
//   - VoteWitness / WitnessUpdate / WitnessCreate：私链 witness 与主网不同
//   - WithdrawBalance：投票奖励金额无法跟主网一致，会级联影响后续交易
//
// 用 --include-all 关闭此过滤。
var defaultSkipContractTypes = map[string]struct{}{
	"VoteWitnessContract":     {},
	"WitnessUpdateContract":   {},
	"WitnessCreateContract":   {},
	"WithdrawBalanceContract": {},
}

// txPeek 只解 txID + contract.type，避免把整个交易 unmarshal 一遍。
type txPeek struct {
	TxID    string `json:"txID"`
	RawData struct {
		Contract []struct {
			Type string `json:"type"`
		} `json:"contract"`
	} `json:"raw_data"`
}

// shouldSkip 返回跳过原因（空字符串表示不跳过）+ 解析出的 peek 信息。
//
// 调用方拿 peek 是为了写日志时附带 txID，避免再 unmarshal 一遍。
func shouldSkip(tx json.RawMessage, skipTypes map[string]struct{}) (string, txPeek) {
	var p txPeek
	if err := json.Unmarshal(tx, &p); err != nil {
		return "parse_error", p
	}
	if len(p.RawData.Contract) == 0 {
		return "no_contract", p
	}
	ctype := p.RawData.Contract[0].Type
	if _, ok := skipTypes[ctype]; ok {
		return "skip_type:" + ctype, p
	}
	return "", p
}
