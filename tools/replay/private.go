package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const privateNodeTimeoutSec = 5

// PrivateNodeClient 把主网交易转发给私链节点的 HTTP API。
type PrivateNodeClient struct {
	baseURL string
	client  *http.Client
}

// broadcastResp 是 /wallet/broadcasttransaction 的响应结构。
type broadcastResp struct {
	Result  bool   `json:"result"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newPrivateNodeClient(baseURL string) *PrivateNodeClient {
	return &PrivateNodeClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: privateNodeTimeoutSec * time.Second},
	}
}

// broadcast 直接把主网 tx json 转发给私链，不做任何字段改写。
// 返回 (success, message)。success=true 表示节点接受了交易；
// 落块成功需 getTransactionInfoById 单独确认，本工具不做。
func (c *PrivateNodeClient) broadcast(ctx context.Context, tx json.RawMessage) (bool, string) {
	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/wallet/broadcasttransaction", bytes.NewReader(tx))
	if err != nil {
		return false, "BUILD_REQ_ERROR: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return false, "NETWORK_ERROR: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r broadcastResp
	if err := json.Unmarshal(body, &r); err != nil {
		return false, "PARSE_ERROR: " + string(body)
	}
	if r.Result {
		return true, "OK"
	}
	msg := r.Message
	// java-tron message 字段常是 hex-encoded，解一下提高可读性
	if msg != "" {
		if decoded, err := hex.DecodeString(msg); err == nil {
			msg = string(decoded)
		}
	}
	return false, r.Code + ": " + msg
}
