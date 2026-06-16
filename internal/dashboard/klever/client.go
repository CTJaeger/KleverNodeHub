package klever

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIURLs returns the indexer (api) and node API base URLs for a network.
// Unknown networks fall back to mainnet.
func DefaultAPIURLs(network string) (apiURL, nodeURL string) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "testnet":
		return "https://api.testnet.klever.org", "https://node.testnet.klever.org"
	default:
		return "https://api.mainnet.klever.org", "https://node.mainnet.klever.org"
	}
}

// Client is a thin HTTP client over the Klever indexer + node APIs.
//
// A buffered semaphore caps concurrent in-flight requests because Klever
// rate-limits per IP; the monitor shares that budget across its calls.
type Client struct {
	http    *http.Client
	apiURL  string
	nodeURL string
	sem     chan struct{}
}

// NewClient builds a Client. apiURL serves blocks/validators, nodeURL serves the
// chain overview. maxInflight caps concurrent requests (min 1).
func NewClient(apiURL, nodeURL string, maxInflight int) *Client {
	if maxInflight < 1 {
		maxInflight = 1
	}
	return &Client{
		http:    &http.Client{Timeout: 15 * time.Second},
		apiURL:  strings.TrimRight(apiURL, "/"),
		nodeURL: strings.TrimRight(nodeURL, "/"),
		sem:     make(chan struct{}, maxInflight),
	}
}

// Overview returns the chain epoch/slot clock (node API).
func (c *Client) Overview(ctx context.Context) (*Overview, error) {
	var env overviewEnvelope
	if err := c.getJSON(ctx, c.nodeURL+"/node/overview", &env); err != nil {
		return nil, err
	}
	return &env.Data.Overview, nil
}

// BlockByNonce returns a single block with its producer and consensus group.
func (c *Client) BlockByNonce(ctx context.Context, nonce uint64) (*IndexerBlock, error) {
	url := fmt.Sprintf("%s/v1.0/block/by-nonce/%d", c.apiURL, nonce)
	var env indexerBlockEnvelope
	if err := c.getJSON(ctx, url, &env); err != nil {
		return nil, err
	}
	return &env.Data.Block, nil
}

// Validators returns the validator list across pages (capped at 8 pages of 100,
// i.e. the full set in practice). Returning every entry — not just elected ones —
// lets the monitor resolve a managed validator's stats whatever its state
// (elected, waiting, or jailed).
func (c *Client) Validators(ctx context.Context) ([]RawValidator, error) {
	const pageSize = 100
	var all []RawValidator
	for page := 1; page <= 8; page++ {
		url := fmt.Sprintf("%s/v1.0/validator/list?page=%d&pageSize=%d", c.apiURL, page, pageSize)
		var env validatorListEnvelope
		if err := c.getJSON(ctx, url, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Data.Validators...)
		if len(env.Data.Validators) < pageSize {
			break // last page
		}
	}
	return all, nil
}

// getJSON performs a GET through the in-flight limiter with a couple of quick
// retries on 429/503. It stays short on purpose: when the per-IP limit is hot,
// failing fast and letting the next poll retry keeps the monitor responsive.
func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	const maxRetries = 2
	for attempt := 0; ; attempt++ {
		select {
		case c.sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		status, body, err := c.do(ctx, url)
		<-c.sem
		if err != nil {
			return err
		}
		if (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) && attempt < maxRetries {
			wait := time.Duration(250*(1<<attempt)) * time.Millisecond
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if status != http.StatusOK {
			return fmt.Errorf("klever GET %s: HTTP %d", url, status)
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode %s: %w", url, err)
		}
		return nil
	}
}

func (c *Client) do(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
