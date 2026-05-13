package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	trongridDefaultURL = "https://api.trongrid.io"
	trongridDefaultQPS = 1 // 每秒拉一个区块够用（主网 3s 一块，3x 速度重放）
	trongridTimeoutSec = 10
	retryMax           = 3
	retryBackoffSec    = 2
)

// Block 仅解 number + 保留 transactions 原始 json，避免无谓的反序列化。
type Block struct {
	BlockHeader struct {
		RawData struct {
			Number int64 `json:"number"`
		} `json:"raw_data"`
	} `json:"block_header"`
	Transactions []json.RawMessage `json:"transactions"`
}

// TronGridClient 封装对 TronGrid HTTP API 的访问，含内置速率限制。
type TronGridClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
	ticker  *time.Ticker // 速率限制：每 1/qps 秒放一个请求过去
}

func newTronGridClient(baseURL, apiKey string, qps int) *TronGridClient {
	if qps <= 0 {
		qps = trongridDefaultQPS
	}
	interval := time.Second / time.Duration(qps)
	return &TronGridClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: trongridTimeoutSec * time.Second},
		ticker:  time.NewTicker(interval),
	}
}

// post 是底层请求方法，统一过速率门 + 注入 API key。
func (c *TronGridClient) post(ctx context.Context, path string, body any) ([]byte, error) {
	<-c.ticker.C
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// getBlock 拉取指定高度区块，含重试 + 退避。返回 nil 表示区块不存在。
func (c *TronGridClient) getBlock(ctx context.Context, num int64) (*Block, error) {
	var lastErr error
	for attempt := 0; attempt < retryMax; attempt++ {
		body, err := c.post(ctx, "/wallet/getblockbynum",
			map[string]int64{"num": num})
		if err == nil {
			var blk Block
			if jerr := json.Unmarshal(body, &blk); jerr != nil {
				return nil, jerr
			}
			// 空响应 / 区块不存在
			if blk.BlockHeader.RawData.Number == 0 && len(blk.Transactions) == 0 {
				return nil, nil
			}
			return &blk, nil
		}
		lastErr = err
		log.Printf("get_block(%d) attempt %d failed: %v", num, attempt+1, err)
		if attempt < retryMax-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(retryBackoffSec*(attempt+1)) * time.Second):
			}
		}
	}
	return nil, lastErr
}

// getNowBlockNum 取主网当前最高块号。
func (c *TronGridClient) getNowBlockNum(ctx context.Context) (int64, error) {
	body, err := c.post(ctx, "/wallet/getnowblock", map[string]any{})
	if err != nil {
		return 0, err
	}
	var blk Block
	if err := json.Unmarshal(body, &blk); err != nil {
		return 0, err
	}
	return blk.BlockHeader.RawData.Number, nil
}
