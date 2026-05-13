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
    // mainnetBlockSec 主网出块间隔；每个主网块就是 3s 的主网时间。
    mainnetBlockSec = 3

    // tpsMultiplierDefault 默认私链 TPS ≈ 1/3 主网 TPS。
    // 对应 pace ≈ 9 秒，即每主网块铺到 9s 私链时间。
    // 适用 3 SR 私链对 27 SR 主网的场景（私链每槽位负担约 9 倍主网密度）。
    // 用 0.333 而非 1.0/3.0 是为了 --help 显示更干净（pace 实际 9.009s 与 9s 无差别）。
    tpsMultiplierDefault = 0.333
)

// Replayer 是主重放循环的状态机：
//   - 拉主网块 → 过滤 → 按节奏广播到私链 → 推进 state
//   - 失败 / 跳过的交易落 jsonl 日志
//   - 支持 Ctrl+C 安全退出 + state 持久化续跑
type Replayer struct {
    cfg       Config
    trongrid  *TronGridClient
    private   *PrivateNodeClient
    state     ReplayState
    skipTypes map[string]struct{}
    failLog   *jsonlLogger
    skipLog   *jsonlLogger
}

// defaultEndOffset 不显式指定 --end 时，默认只跑 start + 这个偏移量。
// 避免一不小心把"到主网最高块"作为默认，导致跑几小时停不下来。
// 想跑大区间显式 --end 即可。
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
            // 不推进 LastMainnetBlock；但保存最新累计计数器供排查
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

// resolveStartBlock 决定从哪个**主网**块开始抓。
//
// 优先级：
//  1. --start 显式指定
//  2. state 文件里上次断点 + 1
//
// 注意：**绝对不能**用私链 getnowblock + 1 作为起点。私链自己也在
// 出块，head 是私链自己的计数，与主网块号无关；第一次启动必须由
// 用户显式提供 --start（通常是 snapshot 裁剪时的最后一个主网块 + 1）。
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

// processBlock 处理单个主网块：拉取 → 按"槽"分批发送到私链。
//
// 节奏算法：
//   - paceTotal = mainnetBlockSec / TpsMultiplier
//     例：multiplier=1   → pace=3s
//         multiplier=0.5 → pace=6s
//         multiplier=1/3 → pace=9s
//   - slots = max(1, floor(paceTotal / 1s))，每槽约 1 秒
//     例：pace=3s → 3 槽；pace=9s → 9 槽；pace=1.5s → 1 槽
//   - n 笔交易尽量均分到这些槽里：前 (n%slots) 个槽各 ceil(n/slots) 笔
//   - 每个槽内交易**连续发送**无 sleep，整批完后等到该槽的绝对边界
//   - 总定时器数：slots 次（典型 3-9 次），而非 n 次（典型 150 次）
//
// 拉块失败仍等满 paceTotal 保持节奏；空块 / 区块不存在直接 return，
// 不占整块时间，让重放更快推进。
//
// 返回 (blockOk, blockFail, blockSkip, err)。
// err 非 nil 当且仅当：本块发起过广播 (attempted > 0) 但全部失败 (ok == 0)。
// 此时调用方 (Run) 应当停服，避免在私链状态有问题时盲目推进 state。
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
        return 0, 0, 0, nil // 拉块失败属于 TronGrid 侧问题，不算"全失败"，继续下一块
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

    // 槽数约等于 paceTotal 的整数秒；slotDuration = paceTotal/slots
    // 吸收小数秒余量，保证最后一个槽 deadline 恰好是 blockStart + paceTotal
    slots := int(paceTotal / time.Second)
    if slots < 1 {
        slots = 1
    }
    slotDuration := paceTotal / time.Duration(slots)
    baseSize := n / slots
    remainder := n % slots // 前 remainder 个槽多分 1 笔

    idx := 0
    for s := 0; s < slots; s++ {
        size := baseSize
        if s < remainder {
            size++
        }
        // 本槽连续发送 size 笔（无 sleep）
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
        // 等到该槽的绝对边界（避免漂移）
        target := blockStart.Add(slotDuration * time.Duration(s+1))
        r.waitUntil(ctx, target)
    }

    blockOk := atomic.LoadInt64(&r.state.TotalBroadcastOk) - okBefore
    blockFail := atomic.LoadInt64(&r.state.TotalBroadcastFail) - failBefore
    blockSkip := atomic.LoadInt64(&r.state.TotalSkipped) - skipBefore

    // 广播试过 (blockFail > 0) 但全失败 (blockOk == 0) → 触发停服
    // 跳过的（VoteWitness 等）不算在内，因为根本没尝试发出去
    if ctx.Err() == nil && blockFail > 0 && blockOk == 0 {
        return blockOk, blockFail, blockSkip,
            fmt.Errorf("block %d: all %d broadcast attempts failed", num, blockFail)
    }
    return blockOk, blockFail, blockSkip, nil
}

// processTx 处理单笔交易：先过滤，过则广播。
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

// waitUntil 阻塞直到 deadline 或 ctx 取消；已过时刻立刻返回。
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
