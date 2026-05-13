// TRON Mainnet → Private Chain Transaction Replayer.
//
// 流程：
//  1. 从 TronGrid API 拉取主网区块
//  2. 提取每个区块里的交易
//  3. 直接 POST 到私链节点的 /wallet/broadcasttransaction
//  4. 失败 / 跳过写日志，成功推进 state.json 断点
//
// 私链节点必须运行 relay_skip_signature 分支
// （跳过 refBlockHash / expiration / TaPos / 部分签名校验）。
//
// 用法：见同目录 README.md
//
// 依赖：纯 Go 标准库
// Go 版本：1.21+
//
// 文件组织：
//  - main.go      入口、CLI、Config
//  - state.go     state 文件读写
//  - trongrid.go  TronGrid 客户端
//  - private.go   私链广播客户端
//  - filter.go    交易过滤规则
//  - logger.go    jsonl 日志写入
//  - replayer.go  主循环、节奏控制
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// Config 是命令行参数 + 环境变量解析出的运行时配置。
type Config struct {
	TrongridURL   string
	TrongridKey   string
	TrongridQPS   int
	TpsMultiplier float64 // 私链 TPS = 主网 TPS × 此倍率；pace_sec = 3 / multiplier
	PrivateNode   string
	Start         int64
	End           int64
	StateFile     string
	FailLog       string
	SkipLog       string
	IncludeAll    bool
}

func parseArgs() Config {
	var c Config
	flag.StringVar(&c.TrongridURL, "trongrid-url", trongridDefaultURL, "TronGrid API base URL")
	flag.StringVar(&c.TrongridKey, "trongrid-key", os.Getenv("TRONGRID_API_KEY"),
		"TronGrid API key (or env TRONGRID_API_KEY)")
	flag.IntVar(&c.TrongridQPS, "trongrid-qps", trongridDefaultQPS,
		"TronGrid request rate limit (queries per second)")
	flag.Float64Var(&c.TpsMultiplier, "tps-multiplier", tpsMultiplierDefault,
		"Private chain TPS as a fraction of mainnet TPS. "+
			"pace_sec = 3 / multiplier. "+
			"Examples: 1 → 3s/block (mainnet speed), 0.5 → 6s/block, "+
			"default ~0.333 → 9s/block (3 SR private vs 27 SR mainnet)")
	flag.StringVar(&c.PrivateNode, "private-node", "",
		"Private chain HTTP API base, e.g. http://10.0.0.1:8090 (required)")
	flag.Int64Var(&c.Start, "start", 0,
		"Start MAINNET block (inclusive). 0 = resume from state file "+
			"(required on first run; do NOT use private chain head)")
	flag.Int64Var(&c.End, "end", 0,
		"End block (inclusive). 0 = start + 10 (short range for safety; "+
			"set explicitly for longer runs)")
	flag.StringVar(&c.StateFile, "state-file", "./replay-state.json", "Resume state file")
	flag.StringVar(&c.FailLog, "fail-log", "./replay-failures.jsonl", "Failed broadcast log")
	flag.StringVar(&c.SkipLog, "skip-log", "./replay-skips.jsonl", "Skipped txs log")
	flag.BoolVar(&c.IncludeAll, "include-all", false,
		"Do not skip Vote/Witness/Withdraw txs (default skips them, see 0.5.3)")
	flag.Parse()
	if c.PrivateNode == "" {
		fmt.Fprintln(os.Stderr, "error: --private-node is required")
		flag.Usage()
		os.Exit(2)
	}
	return c
}

// keysOf 返回 map 的所有键，仅用于启动日志。
func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func main() {
	cfg := parseArgs()

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	skipTypes := map[string]struct{}{}
	if !cfg.IncludeAll {
		skipTypes = defaultSkipContractTypes
	}

	failLog, err := openJsonlLogger(cfg.FailLog)
	if err != nil {
		log.Fatalf("open fail log: %v", err)
	}
	defer failLog.Close()
	skipLog, err := openJsonlLogger(cfg.SkipLog)
	if err != nil {
		log.Fatalf("open skip log: %v", err)
	}
	defer skipLog.Close()

	r := &Replayer{
		cfg:       cfg,
		trongrid:  newTronGridClient(cfg.TrongridURL, cfg.TrongridKey, cfg.TrongridQPS),
		private:   newPrivateNodeClient(cfg.PrivateNode),
		state:     loadState(cfg.StateFile),
		skipTypes: skipTypes,
		failLog:   failLog,
		skipLog:   skipLog,
	}

	if err := r.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run: %v", err)
	}
}
