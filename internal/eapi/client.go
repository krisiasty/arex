package eapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is an eAPI JSON-RPC client for a single switch.
type Client struct {
	httpClient *http.Client
	url        string
	username   string
	password   string
}

// NewClient creates a new eAPI client for one switch.
//
// It fails rather than returning a client when the TLS options are
// unusable -- an unreadable CA bundle or a malformed pin -- so the problem
// surfaces at startup instead of as a per-request error on every poll.
func NewClient(host, username, password string, timeout time.Duration, tlsOpts TLSOptions) (*Client, error) {
	tlsCfg, err := buildTLSConfig(tlsOpts)
	if err != nil {
		return nil, err
	}
	return &Client{
		url:      host + "/command-api",
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

type request struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  params `json:"params"`
	ID      int    `json:"id"`
}

type params struct {
	Version int      `json:"version"`
	Cmds    []string `json:"cmds"`
	Format  string   `json:"format"`
}

type Response struct {
	Result []json.RawMessage `json:"result"`
	Error  *rpcError         `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CommandError means the switch answered and then rejected the request --
// typically a command it does not support. It is distinct from a transport
// or authentication failure: retrying commands individually can recover the
// ones the switch does accept, whereas an unreachable switch will reject
// every attempt and retrying just multiplies the timeout.
type CommandError struct {
	Code    int
	Message string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("eAPI error %d: %s", e.Code, e.Message)
}

// Run executes a list of EOS CLI commands and returns the raw JSON results,
// one entry per command in the same order.
func (c *Client) Run(cmds []string) ([]json.RawMessage, error) {
	req := request{
		Jsonrpc: "2.0",
		Method:  "runCmds",
		Params: params{
			Version: 1,
			Cmds:    cmds,
			Format:  "json",
		},
		ID: 1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.SetBasicAuth(c.username, c.password)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, &CommandError{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message}
	}

	return rpcResp.Result, nil
}
