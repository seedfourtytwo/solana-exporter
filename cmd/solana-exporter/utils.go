package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/seedfourtytwo/solana-exporter/pkg/rpc"
	"github.com/seedfourtytwo/solana-exporter/pkg/slog"
)

const VoteProgram = "Vote111111111111111111111111111111111111111"

type EpochTrackedValidators struct {
	trackedNodekeys map[int64]map[string]struct{}
	mu              sync.RWMutex
}

func NewEpochTrackedValidators() *EpochTrackedValidators {
	return &EpochTrackedValidators{
		trackedNodekeys: make(map[int64]map[string]struct{}),
	}
}

func (c *EpochTrackedValidators) GetTrackedValidators(epoch int64) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// get and delete from tracked map:
	epochNodekeys, ok := c.trackedNodekeys[epoch]
	if !ok {
		return nil, fmt.Errorf("epoch %v not tracked", epoch)
	}
	delete(c.trackedNodekeys, epoch)

	// convert to array:
	var nodekeys []string
	for nodekey := range epochNodekeys {
		nodekeys = append(nodekeys, nodekey)
	}
	return nodekeys, nil
}

func (c *EpochTrackedValidators) AddTrackedNodekeys(epoch int64, nodekeys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	epochNodekeys, ok := c.trackedNodekeys[epoch]
	if !ok {
		epochNodekeys = make(map[string]struct{})
	}
	for _, nodekey := range nodekeys {
		epochNodekeys[nodekey] = struct{}{}
	}
	c.trackedNodekeys[epoch] = epochNodekeys
}

func assertf(condition bool, format string, args ...any) {
	logger := slog.Get()
	if !condition {
		logger.Fatalf(format, args...)
	}
}

// toString is just a simple utility function for converting to strings
func toString(i any) string {
	return fmt.Sprintf("%v", i)
}

// SelectFromSchedule takes a leader-schedule and returns a trimmed leader-schedule
// containing only the slots within the provided range
func SelectFromSchedule(schedule map[string][]int64, startSlot, endSlot int64) map[string][]int64 {
	selected := make(map[string][]int64)
	for key, values := range schedule {
		var selectedValues []int64
		for _, value := range values {
			if value >= startSlot && value <= endSlot {
				selectedValues = append(selectedValues, value)
			}
		}
		selected[key] = selectedValues
	}
	return selected
}

// GetTrimmedLeaderSchedule fetches the leader schedule, but only for the validators we are interested in.
// Additionally, it adjusts the leader schedule to the current epoch offset.
func GetTrimmedLeaderSchedule(
	ctx context.Context, client *rpc.Client, identities []string, slot, epochFirstSlot int64,
) (map[string][]int64, error) {
	logger := slog.Get()
	leaderSchedule, err := client.GetLeaderSchedule(ctx, rpc.CommitmentConfirmed, slot)
	if err != nil {
		return nil, fmt.Errorf("failed to get leader schedule: %w", err)
	}

	trimmedLeaderSchedule := make(map[string][]int64)
	for _, id := range identities {
		if leaderSlots, ok := leaderSchedule[id]; ok {
			// when you fetch the leader schedule, it gives you slot indexes, we want absolute slots:
			absoluteSlots := make([]int64, len(leaderSlots))
			for i, slotIndex := range leaderSlots {
				absoluteSlots[i] = slotIndex + epochFirstSlot
			}
			trimmedLeaderSchedule[id] = absoluteSlots
		} else {
			logger.Warnf("failed to find leader slots for %v", id)
		}
	}

	return trimmedLeaderSchedule, nil
}

// GetAssociatedVoteAccounts returns the votekeys associated with a given list of nodekeys
func GetAssociatedVoteAccounts(
	ctx context.Context, client *rpc.Client, commitment rpc.Commitment, nodekeys []string,
) ([]string, error) {
	voteAccounts, err := client.GetVoteAccounts(ctx, commitment)
	if err != nil {
		return nil, err
	}

	// first map nodekey -> votekey:
	voteAccountsMap := make(map[string]string)
	for _, voteAccount := range append(voteAccounts.Current, voteAccounts.Delinquent...) {
		voteAccountsMap[voteAccount.NodePubkey] = voteAccount.VotePubkey
	}

	votekeys := make([]string, len(nodekeys))
	for i, nodeKey := range nodekeys {
		votekey := voteAccountsMap[nodeKey]
		if votekey == "" {
			return nil, fmt.Errorf("failed to find vote key for node %v", nodeKey)
		}
		votekeys[i] = votekey
	}
	return votekeys, nil
}

// FetchBalances fetches SOL balances for a list of addresses
func FetchBalances(ctx context.Context, client *rpc.Client, addresses []string) (map[string]float64, error) {
	balances := make(map[string]float64)
	for _, address := range addresses {
		balance, err := client.GetBalance(ctx, rpc.CommitmentConfirmed, address)
		if err != nil {
			return nil, err
		}
		balances[address] = balance
	}
	return balances, nil
}

// CombineUnique combines unique items from multiple arrays to a single array.
func CombineUnique[T comparable](args ...[]T) []T {
	var uniqueItems []T
	for _, arg := range args {
		for _, item := range arg {
			if !slices.Contains(uniqueItems, item) {
				uniqueItems = append(uniqueItems, item)
			}
		}
	}
	return uniqueItems
}

// GetEpochBounds returns the first slot and last slot within an [inclusive] Epoch
func GetEpochBounds(info *rpc.EpochInfo) (int64, int64) {
	firstSlot := info.AbsoluteSlot - info.SlotIndex
	return firstSlot, firstSlot + info.SlotsInEpoch - 1
}

func CountVoteTransactions(block *rpc.Block) (int, error) {
	txData, err := json.Marshal(block.Transactions)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal transactions: %w", err)
	}
	var transactions []rpc.FullTransaction
	if err := json.Unmarshal(txData, &transactions); err != nil {
		return 0, fmt.Errorf("failed to unmarshal transactions: %w", err)
	}

	voteCount := 0
	for _, tx := range transactions {
		if slices.Contains(tx.Transaction.Message.AccountKeys, VoteProgram) {
			voteCount++
		}
	}
	return voteCount, nil
}

// BoolToFloat64 converts a boolean to either 1.0 or 0.0
func BoolToFloat64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ExtractHealthAndNumSlotsBehind takes the outputs from the GetHealth RPC method and determines the corresponding
// health status and number of slots behind, along with potential errors corresponding to each metric
func ExtractHealthAndNumSlotsBehind(health string, getHealthErr error) (
	isHealthy bool, isHealthyErr error, numSlotsBehind int64, numSlotsBehindErr error,
) {
	// for an unhealthy node:
	if health != "ok" {
		// first check this unexpected edge case: whenever we don't get "ok" from the
		// health check, we should get an error
		if getHealthErr == nil {
			// if this happens, return and error for both values:
			err := fmt.Errorf("health check did not return 'ok' (%s) but no error", health)
			return false, err, 0, err
		}

		// now from here on, we just have to handle the error, first check if it's some random error
		// and not an unhealthy-node error:
		var rpcError *rpc.Error
		if ok := errors.As(getHealthErr, &rpcError); !ok || rpcError.Code != rpc.NodeUnhealthyCode {
			err := fmt.Errorf("failed to call getHealth: %w", getHealthErr)
			return false, err, 0, err
		}

		// from here, this must be a node-unhealthy error, so now we check if it's generic or not
		// see docs (https://solana.com/docs/rpc/http/gethealth)
		if rpcError.Data == nil {
			// this is the generic case:
			// TODO: in this generic case, do we want to emit an error to the solana_node_num_slots_behind metric?
			//  The node is definitely unhealthy, but we do not have the information to determine what numSlotsBehind is,
			//  so do we say 0 or error?
			return false, nil, 0, fmt.Errorf("unhealthy node but cannot determine numSlotsBehind: %w", getHealthErr)
		}

		var errorData rpc.NodeUnhealthyErrorData
		if err := rpc.UnpackRpcErrorData(rpcError, &errorData); err != nil {
			// if we error here, it means we have the incorrect format:
			return false, nil, 0, fmt.Errorf("failed to unpack RPC error data: %w", err)
		}

		// if it unpacked correctly, then just return the numSlotsBehind:
		return false, nil, errorData.NumSlotsBehind, nil
	}

	// now for a healthy node, first check an edge case which is unexpected to happen; whenever we have "ok",
	// we shouldn't be getting an error
	if getHealthErr != nil {
		// if this happens, return and error for both values:
		err := fmt.Errorf("health check returned 'ok' and error: %w", getHealthErr)
		return false, err, 0, err
	}

	// in this expected case, we are healthy + no error:
	return true, nil, 0, nil

}

// GetVoteAccountFromIdentity finds the vote account pubkey associated with a validator identity
func GetVoteAccountFromIdentity(ctx context.Context, client *rpc.Client, identity string) (string, error) {
	voteAccounts, err := client.GetVoteAccounts(ctx, rpc.CommitmentConfirmed)
	if err != nil {
		return "", fmt.Errorf("failed to get vote accounts: %w", err)
	}
	
	// Search in both current and delinquent validators
	for _, accounts := range [][]rpc.VoteAccount{voteAccounts.Current, voteAccounts.Delinquent} {
		for _, account := range accounts {
			if account.NodePubkey == identity {
				return account.VotePubkey, nil
			}
		}
	}
	
	return "", fmt.Errorf("no vote account found for identity %s", identity)
}
