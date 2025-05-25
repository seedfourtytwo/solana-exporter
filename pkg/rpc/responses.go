package rpc

import (
	"encoding/json"
	"fmt"
)

type (
	Error struct {
		Message string         `json:"message"`
		Code    int64          `json:"code"`
		Data    map[string]any `json:"data"`
		// Method is not returned by the RPC, rather added by the client for visibility purposes
		Method string
	}

	Response[T any] struct {
		Jsonrpc string `json:"jsonrpc"`
		Result  T      `json:"result,omitempty"`
		Error   Error  `json:"error,omitempty"`
		Id      int    `json:"id"`
	}

	contextualResult[T any] struct {
		Value   T `json:"value"`
		Context struct {
			Slot int64 `json:"slot"`
		} `json:"context"`
	}

	EpochInfo struct {
		AbsoluteSlot     int64 `json:"absoluteSlot"`
		BlockHeight      int64 `json:"blockHeight"`
		Epoch            int64 `json:"epoch"`
		SlotIndex        int64 `json:"slotIndex"`
		SlotsInEpoch     int64 `json:"slotsInEpoch"`
		TransactionCount int64 `json:"transactionCount"`
	}

	VoteAccount struct {
		ActivatedStake int64  `json:"activatedStake"`
		LastVote       int    `json:"lastVote"`
		NodePubkey     string `json:"nodePubkey"`
		RootSlot       int    `json:"rootSlot"`
		VotePubkey     string `json:"votePubkey"`
		Credits        int64  `json:"credits"`         // Current epoch credits
		EpochCredits   [][]int64 `json:"epochCredits"`   // Array of [epoch, credits, previous_credits]
		EpochVoteAccount bool `json:"epochVoteAccount"` // Whether this is the current epoch's vote account
		Commission     int   `json:"commission"`     // The validator's commission percentage
	}

	VoteAccounts struct {
		Current    []VoteAccount `json:"current"`
		Delinquent []VoteAccount `json:"delinquent"`
	}

	HostProduction struct {
		LeaderSlots    int64
		BlocksProduced int64
	}

	BlockProductionRange struct {
		FirstSlot int64 `json:"firstSlot"`
		LastSlot  int64 `json:"lastSlot"`
	}

	BlockProduction struct {
		ByIdentity map[string]HostProduction `json:"byIdentity"`
		Range      BlockProductionRange      `json:"range"`
	}

	InflationReward struct {
		Amount int64 `json:"amount"`
		Epoch  int64 `json:"epoch"`
	}

	Block struct {
		Rewards      []BlockReward    `json:"rewards"`
		Transactions []map[string]any `json:"transactions"`
	}

	BlockReward struct {
		Pubkey     string `json:"pubkey"`
		Lamports   int64  `json:"lamports"`
		RewardType string `json:"rewardType"`
	}

	FullTransaction struct {
		Transaction struct {
			Message struct {
				AccountKeys []string `json:"accountKeys"`
			} `json:"message"`
		} `json:"transaction"`
	}

	ValidatorCredits struct {
		CurrentEpochCredits int64 `json:"currentEpochCredits"`
		TotalCredits       int64 `json:"totalCredits"`
	}
)

func (e *Error) Error() string {
	return fmt.Sprintf("%s rpc error (code: %d): %s (data: %v)", e.Method, e.Code, e.Message, e.Data)
}

func (hp *HostProduction) UnmarshalJSON(data []byte) error {
	var arr []int64
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	if len(arr) != 2 {
		return fmt.Errorf("expected array of 2 integers, got %d", len(arr))
	}
	hp.BlocksProduced = arr[0] // produced
	hp.LeaderSlots = arr[1]    // assigned
	return nil
}

func (v *VoteAccount) GetValidatorCredits() (int64, int64) {
	if len(v.EpochCredits) == 0 {
		return 0, 0
	}

	// Get the last entry in EpochCredits which represents the current epoch
	lastEntry := v.EpochCredits[len(v.EpochCredits)-1]
	if len(lastEntry) < 3 {
		return 0, 0
	}

	// lastEntry[0] = epoch
	// lastEntry[1] = credits (current total)
	// lastEntry[2] = previous_credits (total from previous epoch)
	currentEpochCredits := lastEntry[1] - lastEntry[2] // Credits earned in current epoch
	totalCredits := lastEntry[1] // Total credits across all epochs

	return currentEpochCredits, totalCredits
}
