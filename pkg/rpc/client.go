package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"time"
	"sync"

	"github.com/seedfourtytwo/solana-exporter/pkg/slog"
	"go.uber.org/zap"
	"github.com/prometheus/client_golang/prometheus"
)

type (
	Client struct {
		HttpClient  http.Client
		RpcUrl      string
		HttpTimeout time.Duration
		logger      *zap.SugaredLogger
	}

	Request struct {
		Jsonrpc string `json:"jsonrpc"`
		Id      int    `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}

	Commitment string
)

const (
	// LamportsInSol is the number of lamports in 1 SOL (a billion)
	LamportsInSol = 1_000_000_000
	// CommitmentFinalized level offers the highest level of certainty for a transaction on the Solana blockchain.
	// A transaction is considered "Finalized" when it is included in a block that has been confirmed by a
	// supermajority of the stake, and at least 31 additional confirmed blocks have been built on top of it.
	CommitmentFinalized Commitment = "finalized"
	// CommitmentConfirmed level is reached when a transaction is included in a block that has been voted on
	// by a supermajority (66%+) of the network's stake.
	CommitmentConfirmed Commitment = "confirmed"
	// CommitmentProcessed level represents a transaction that has been received by the network and included in a block.
	CommitmentProcessed Commitment = "processed"

	DevnetGenesisHash  = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	TestnetGenesisHash = "4uhcVJyU9pJkvQyS88uRDiswHXSCkY3zQawwpjk2NsNY"
	MainnetGenesisHash = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
)

// Global map to count RPC calls per method
var rpcCallCounts = make(map[string]*int64)
var rpcCallCountsLock = make(chan struct{}, 1)

// Prometheus metric for counting RPC calls by method
var RpcCallCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "solana_exporter_rpc_calls_total",
		Help: "Total number of Solana RPC calls made, labeled by method.",
	},
	[]string{"method"},
)

// EpochInfo cache and mutex
var (
	epochInfoCache      *EpochInfo
	epochInfoCacheTime  time.Time
	epochInfoCacheMutex sync.Mutex
)

// MinimumLedgerSlot cache and mutex
var (
	minimumLedgerSlotCache     int64
	minimumLedgerSlotCacheTime time.Time
	minimumLedgerSlotCacheSet  bool
	minimumLedgerSlotCacheMutex sync.Mutex
)

// FirstAvailableBlock cache and mutex
var (
	firstAvailableBlockCache     int64
	firstAvailableBlockCacheTime time.Time
	firstAvailableBlockCacheSet  bool
	firstAvailableBlockCacheMutex sync.Mutex
)

func init() {
	// Start a goroutine to log the counts every minute
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			rpcCallCountsLock <- struct{}{} // lock
			logger := slog.Get()
			logger.Infof("=== SOLANA RPC CALLS IN LAST MINUTE ===")
			for method, countPtr := range rpcCallCounts {
				count := atomic.SwapInt64(countPtr, 0)
				logger.Infof("%s: %d", method, count)
			}
			<-rpcCallCountsLock // unlock
		}
	}()
	prometheus.MustRegister(RpcCallCounter)
}

// GetClusterFromGenesisHash returns the cluster name based on the genesis hash
func GetClusterFromGenesisHash(hash string) (string, error) {
	switch hash {
	case DevnetGenesisHash:
		return "devnet", nil
	case TestnetGenesisHash:
		return "testnet", nil
	case MainnetGenesisHash:
		return "mainnet-beta", nil
	default:
		return "", fmt.Errorf("unknown genesis hash: %s", hash)
	}
}

func NewRPCClient(rpcAddr string, httpTimeout time.Duration) *Client {
	return &Client{HttpClient: http.Client{}, RpcUrl: rpcAddr, HttpTimeout: httpTimeout, logger: slog.Get()}
}

// getResponse is the internal helper for making RPC calls
func getResponse[T any](
	ctx context.Context, client *Client, method string, params []any, rpcResponse *Response[T],
) error {
	// Increment Prometheus counter for this method
	RpcCallCounter.WithLabelValues(method).Inc()
	logger := slog.Get()
	// Count and log the call
	rpcCallCountsLock <- struct{}{} // lock
	if _, ok := rpcCallCounts[method]; !ok {
		var zero int64
		rpcCallCounts[method] = &zero
	}
	atomic.AddInt64(rpcCallCounts[method], 1)
	<-rpcCallCountsLock // unlock
	logger.Debugf("SOLANA RPC CALL: method=%s params=%v", method, params)
	// format request:
	request := &Request{Jsonrpc: "2.0", Id: 1, Method: method, Params: params}
	buffer, err := json.Marshal(request)
	if err != nil {
		logger.Fatalf("failed to marshal request: %v", err)
	}
	logger.Debugf("jsonrpc request: %s", string(buffer))

	// make request:
	ctx, cancel := context.WithTimeout(ctx, client.HttpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", client.RpcUrl, bytes.NewBuffer(buffer))
	if err != nil {
		logger.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("content-type", "application/json")

	resp, err := client.HttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s rpc call failed: %w", method, err)
	}
	//goland:noinspection GoUnhandledErrorResult
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error processing %s rpc call: %w", method, err)
	}
	// debug log response:
	logger.Debugf("%s response: %v", method, string(body))

	// unmarshal the response into the predicted format
	if err = json.Unmarshal(body, rpcResponse); err != nil {
		return fmt.Errorf("failed to decode %s response body: %w", method, err)
	}

	// check for an actual rpc error
	if rpcResponse.Error.Code != 0 {
		rpcResponse.Error.Method = method
		return &rpcResponse.Error
	}
	return nil
}

// GetEpochInfo returns info about the current epoch, with a 15s cache to deduplicate calls.
func (c *Client) GetEpochInfo(ctx context.Context, commitment Commitment) (*EpochInfo, error) {
	epochInfoCacheMutex.Lock()
	defer epochInfoCacheMutex.Unlock()
	if epochInfoCache != nil && time.Since(epochInfoCacheTime) < 15*time.Second {
		return epochInfoCache, nil
	}
	config := map[string]string{"commitment": string(commitment)}
	var resp Response[EpochInfo]
	if err := getResponse(ctx, c, "getEpochInfo", []any{config}, &resp); err != nil {
		return nil, err
	}
	epochInfoCache = &resp.Result
	epochInfoCacheTime = time.Now()
	return epochInfoCache, nil
}

// GetVoteAccounts returns the account info and associated stake for all the voting accounts in the current bank.
// See API docs: https://solana.com/docs/rpc/http/getvoteaccounts
func (c *Client) GetVoteAccounts(ctx context.Context, commitment Commitment) (*VoteAccounts, error) {
	// format params:
	config := map[string]string{"commitment": string(commitment)}
	var resp Response[VoteAccounts]
	if err := getResponse(ctx, c, "getVoteAccounts", []any{config}, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

// GetValidatorCredits returns the current epoch credits and total accumulated credits for a validator
// See API docs: https://solana.com/docs/rpc/http/getvoteaccounts
func (c *Client) GetValidatorCredits(validatorPubkey string) (*ValidatorCredits, error) {
	voteAccounts, err := c.GetVoteAccounts(context.Background(), CommitmentConfirmed)
	if err != nil {
		return nil, fmt.Errorf("failed to get vote accounts: %w", err)
	}

	// Find the current vote account for the validator
	for _, account := range voteAccounts.Current {
		if account.VotePubkey == validatorPubkey {
			currentEpochCredits, totalCredits := account.GetValidatorCredits()
			return &ValidatorCredits{
				CurrentEpochCredits: currentEpochCredits,
				TotalCredits:        totalCredits,
			}, nil
		}
	}

	return nil, fmt.Errorf("validator %s not found in current vote accounts", validatorPubkey)
}

// GetVersion returns the current Solana version running on the node.
// See API docs: https://solana.com/docs/rpc/http/getversion
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	var resp Response[struct {
		Version string `json:"solana-core"`
	}]
	if err := getResponse(ctx, c, "getVersion", []any{}, &resp); err != nil {
		return "", err
	}
	return resp.Result.Version, nil
}

// GetIdentity returns identity pubkey for the current node.
// See API docs: https://solana.com/docs/rpc/http/getidentity
func (c *Client) GetIdentity(ctx context.Context) (string, error) {
	var resp Response[struct {
		Identity string `json:"identity"`
	}]
	if err := getResponse(ctx, c, "getIdentity", []any{}, &resp); err != nil {
		return "", err
	}
	return resp.Result.Identity, nil
}

// GetSlot returns the slot that has reached the given or default commitment level.
// See API docs: https://solana.com/docs/rpc/http/getslot
func (c *Client) GetSlot(ctx context.Context, commitment Commitment) (int64, error) {
	config := map[string]string{"commitment": string(commitment)}
	var resp Response[int64]
	if err := getResponse(ctx, c, "getSlot", []any{config}, &resp); err != nil {
		return 0, err
	}
	return resp.Result, nil
}

// GetBlockProduction returns recent block production information from the current or previous epoch.
// See API docs: https://solana.com/docs/rpc/http/getblockproduction
func (c *Client) GetBlockProduction(
	ctx context.Context, commitment Commitment, firstSlot int64, lastSlot int64,
) (*BlockProduction, error) {
	// format params:
	config := map[string]any{
		"commitment": string(commitment),
		"range":      map[string]int64{"firstSlot": firstSlot, "lastSlot": lastSlot},
	}
	// make request:
	var resp Response[contextualResult[BlockProduction]]
	if err := getResponse(ctx, c, "getBlockProduction", []any{config}, &resp); err != nil {
		return nil, err
	}
	return &resp.Result.Value, nil
}

// GetBalance returns the lamport balance of the account of provided pubkey.
// See API docs:https://solana.com/docs/rpc/http/getbalance
func (c *Client) GetBalance(ctx context.Context, commitment Commitment, address string) (float64, error) {
	config := map[string]string{"commitment": string(commitment)}
	var resp Response[contextualResult[int64]]
	if err := getResponse(ctx, c, "getBalance", []any{address, config}, &resp); err != nil {
		return 0, err
	}
	return float64(resp.Result.Value) / float64(LamportsInSol), nil
}

// GetInflationReward returns the inflation / staking reward for a list of addresses for an epoch.
// See API docs: https://solana.com/docs/rpc/http/getinflationreward
func (c *Client) GetInflationReward(
	ctx context.Context, commitment Commitment, addresses []string, epoch int64,
) ([]InflationReward, error) {
	// format params:
	config := map[string]any{"commitment": string(commitment), "epoch": epoch}
	var resp Response[[]InflationReward]
	if err := getResponse(ctx, c, "getInflationReward", []any{addresses, config}, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// GetLeaderSchedule returns the leader schedule for an epoch.
// See API docs: https://solana.com/docs/rpc/http/getleaderschedule
func (c *Client) GetLeaderSchedule(ctx context.Context, commitment Commitment, slot int64) (map[string][]int64, error) {
	config := map[string]any{"commitment": string(commitment)}
	var resp Response[map[string][]int64]
	if err := getResponse(ctx, c, "getLeaderSchedule", []any{slot, config}, &resp); err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// GetBlock returns identity and transaction information about a confirmed block in the ledger.
// See API docs: https://solana.com/docs/rpc/http/getblock
func (c *Client) GetBlock(
	ctx context.Context, commitment Commitment, slot int64, transactionDetails string,
) (*Block, error) {
	detailsOptions := []string{"full", "none"}
	if !slices.Contains(detailsOptions, transactionDetails) {
		c.logger.Fatalf(
			"%s is not a valid transaction-details option, must be one of %v", transactionDetails, detailsOptions,
		)
	}
	if commitment == CommitmentProcessed {
		// as per https://solana.com/docs/rpc/http/getblock
		c.logger.Fatalf("commitment '%v' is not supported for GetBlock", CommitmentProcessed)
	}
	config := map[string]any{
		"commitment":                     commitment,
		"encoding":                       "json", // this is default, but no harm in specifying it
		"transactionDetails":             transactionDetails,
		"rewards":                        true, // what we here for!
		"maxSupportedTransactionVersion": 0,
	}
	var resp Response[Block]
	if err := getResponse(ctx, c, "getBlock", []any{slot, config}, &resp); err != nil {
		return nil, err
	}
	return &resp.Result, nil
}

// GetHealth returns the current health of the node. A healthy node is one that is within a blockchain-configured slots
// of the latest cluster confirmed slot.
// See API docs: https://solana.com/docs/rpc/http/gethealth
func (c *Client) GetHealth(ctx context.Context) (string, error) {
	var resp Response[string]
	if err := getResponse(ctx, c, "getHealth", []any{}, &resp); err != nil {
		return "", err
	}
	return resp.Result, nil
}

// GetMinimumLedgerSlot returns the lowest slot that the node has information about in its ledger.
// Now uses a 10-minute cache to reduce redundant calls.
func (c *Client) GetMinimumLedgerSlot(ctx context.Context) (int64, error) {
	minimumLedgerSlotCacheMutex.Lock()
	defer minimumLedgerSlotCacheMutex.Unlock()
	if minimumLedgerSlotCacheSet && time.Since(minimumLedgerSlotCacheTime) < 10*time.Minute {
		return minimumLedgerSlotCache, nil
	}
	var resp Response[int64]
	if err := getResponse(ctx, c, "minimumLedgerSlot", []any{}, &resp); err != nil {
		return 0, err
	}
	minimumLedgerSlotCache = resp.Result
	minimumLedgerSlotCacheTime = time.Now()
	minimumLedgerSlotCacheSet = true
	return minimumLedgerSlotCache, nil
}

// GetFirstAvailableBlock returns the slot of the lowest confirmed block that has not been purged from the ledger
// Now uses a 10-minute cache to reduce redundant calls.
func (c *Client) GetFirstAvailableBlock(ctx context.Context) (int64, error) {
	firstAvailableBlockCacheMutex.Lock()
	defer firstAvailableBlockCacheMutex.Unlock()
	if firstAvailableBlockCacheSet && time.Since(firstAvailableBlockCacheTime) < 10*time.Minute {
		return firstAvailableBlockCache, nil
	}
	var resp Response[int64]
	if err := getResponse(ctx, c, "getFirstAvailableBlock", []any{}, &resp); err != nil {
		return 0, err
	}
	firstAvailableBlockCache = resp.Result
	firstAvailableBlockCacheTime = time.Now()
	firstAvailableBlockCacheSet = true
	return firstAvailableBlockCache, nil
}

// GetGenesisHash returns the hash of the genesis block
// See API docs: https://solana.com/docs/rpc/http/getgenesishash
func (c *Client) GetGenesisHash(ctx context.Context) (string, error) {
	var resp Response[string]
	if err := getResponse(ctx, c, "getGenesisHash", []any{}, &resp); err != nil {
		return "", err
	}
	return resp.Result, nil
}
