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
	trongridDefaultQPS = 1 // 1 block/sec is plenty (mainnet produces a block every 3s; this is 3x replay)
	trongridTimeoutSec = 10
	retryMax           = 3
	retryBackoffSec    = 2
)

// Block only decodes the block number and keeps the transactions as raw
// JSON, avoiding an unnecessary deep unmarshal of every transaction.
type Block struct {
	BlockHeader struct {
		RawData struct {
			Number int64 `json:"number"`
		} `json:"raw_data"`
	} `json:"block_header"`
	Transactions []json.RawMessage `json:"transactions"`
}

// TronGridClient wraps the TronGrid HTTP API with a built-in rate limiter.
type TronGridClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
	ticker  *time.Ticker // rate limiter: lets one request through every 1/qps seconds
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

// post is the underlying request method; all calls go through the rate
// limiter and the API key is injected here.
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

// getBlock fetches the block at the given height with retry + backoff.
// A nil return value (without error) means the block does not exist.
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
			// empty response / block does not exist
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

// getNowBlockNum returns the current mainnet head block number.
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
