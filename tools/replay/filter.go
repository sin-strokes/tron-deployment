package main

import "encoding/json"

// defaultSkipContractTypes lists the contract types skipped by default.
//
// These are guaranteed to fail when replayed on a private chain:
//   - VoteWitness / WitnessUpdate / WitnessCreate: the private chain's
//     witness set is different from mainnet's
//   - WithdrawBalance: voting rewards don't match mainnet amounts, which
//     cascades into subsequent balance-dependent transactions
//
// Pass --include-all to disable this filter.
var defaultSkipContractTypes = map[string]struct{}{
	"VoteWitnessContract":     {},
	"WitnessUpdateContract":   {},
	"WitnessCreateContract":   {},
	"WithdrawBalanceContract": {},
}

// txPeek only decodes txID + contract.type, avoiding a full transaction
// unmarshal when we only need to decide whether to skip.
type txPeek struct {
	TxID    string `json:"txID"`
	RawData struct {
		Contract []struct {
			Type string `json:"type"`
		} `json:"contract"`
	} `json:"raw_data"`
}

// shouldSkip returns the skip reason ("" = do not skip) plus the parsed
// peek info.
//
// Callers use the peek to log the txID without unmarshaling again.
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
