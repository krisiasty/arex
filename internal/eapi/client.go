package eapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"time"
)

// Client is an eAPI JSON-RPC client for a single switch.
type Client struct {
	httpClient *http.Client
	url        string
	path       string
	username   string
	password   string

	debug bool
	name  string
}

// NewClient creates a new eAPI client for one switch.
//
// It fails rather than returning a client when the TLS options are
// unusable -- an unreadable CA bundle or a malformed pin -- so the problem
// surfaces at startup instead of as a per-request error on every poll.
func NewClient(host, username, password string, timeout time.Duration,
	tlsOpts TLSOptions, opts ...Option) (*Client, error) {
	tlsCfg, err := buildTLSConfig(tlsOpts)
	if err != nil {
		return nil, err
	}
	c := &Client{
		url:      host + commandAPIPath,
		path:     commandAPIPath,
		username: username,
		password: password,
		name:     host,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// commandAPIPath is where EOS serves eAPI.
const commandAPIPath = "/command-api"

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

// statusError turns an HTTP status into an error naming the likely fix.
// These three are what actually gets hit in the field, and each has a
// different cause, so reporting the bare status leaves the operator guessing.
//
// Deliberately not a CommandError: an HTTP-level rejection applies to every
// command, so retrying them individually would only multiply the failures --
// nine failed authentications per poll instead of one is how account
// lockouts happen.
func statusError(code int, status string) error {
	switch code {
	case http.StatusUnauthorized:
		return fmt.Errorf("eAPI rejected our credentials (%s): check the username and "+
			"password, and that the user has a role permitting the show commands", status)
	case http.StatusForbidden:
		return fmt.Errorf("eAPI refused the request (%s): check for an access-group on "+
			"management api http-commands restricting which sources may connect", status)
	case http.StatusNotFound:
		return fmt.Errorf("eAPI endpoint not found (%s): check that eAPI is enabled, "+
			"including inside the management VRF, and that https is the configured protocol", status)
	default:
		return fmt.Errorf("unexpected HTTP status: %s", status)
	}
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

	ctx := context.Background()
	rl := &requestLog{
		name:     c.name,
		method:   http.MethodPost,
		path:     c.path,
		cmds:     cmds,
		start:    time.Now(),
		reqBytes: len(body),
	}
	if c.debug {
		defer rl.emit()
		ctx = httptrace.WithClientTrace(ctx, rl.trace())
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.SetBasicAuth(c.username, c.password)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		rl.err = err
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	rl.status = resp.StatusCode
	rl.proto = resp.Proto

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode, resp.Status)
	}

	// Count bytes as they are decoded rather than buffering: a single
	// "show interfaces phy detail" is hundreds of kilobytes.
	counted := &countingReader{r: resp.Body}
	var rpcResp Response
	decodeErr := json.NewDecoder(counted).Decode(&rpcResp)
	// Draining lets the connection be reused, and completes the byte count.
	_, _ = io.Copy(io.Discard, counted)
	rl.respByte = counted.n

	if decodeErr != nil {
		rl.err = decodeErr
		return nil, fmt.Errorf("decode response: %w", decodeErr)
	}
	if rpcResp.Error != nil {
		rl.eapiCode = rpcResp.Error.Code
		rl.eapiMsg = rpcResp.Error.Message
		return nil, &CommandError{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message}
	}

	return rpcResp.Result, nil
}
