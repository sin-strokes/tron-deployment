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

// PrivateNodeClient forwards mainnet transactions to the private chain
// node's HTTP API.
type PrivateNodeClient struct {
	baseURL string
	client  *http.Client
}

// broadcastResp is the response shape of /wallet/broadcasttransaction.
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

// broadcast forwards a mainnet tx JSON to the private chain as-is,
// without rewriting any fields.
// Returns (success, message). success=true means the node accepted the
// transaction into its pending pool; confirming the tx actually landed
// in a block requires a separate getTransactionInfoById call, which
// this tool does not perform.
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
	// java-tron's message field is usually hex-encoded; decode for readability
	if msg != "" {
		if decoded, err := hex.DecodeString(msg); err == nil {
			msg = string(decoded)
		}
	}
	return false, r.Code + ": " + msg
}
