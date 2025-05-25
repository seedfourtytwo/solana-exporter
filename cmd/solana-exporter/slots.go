package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/seedfourtytwo/solana-exporter/pkg/slog"
	"go.uber.org/zap"
	"slices"
	"strings"
	"time"

	"github.com/seedfourtytwo/solana-exporter/pkg/rpc"
	"github.com/prometheus/client_golang/prometheus"
)

type SlotWatcher struct {
	client *rpc.Client
	logger *zap.SugaredLogger

	config *ExporterConfig

	// currentEpoch is the current epoch we are watching
	currentEpoch int64
	// firstSlot is the first slot [inclusive] of the current epoch which we are watching
	firstSlot int64
	// lastSlot is the last slot [inclusive] of the current epoch which we are watching
	lastSlot int64
	// slotWatermark is the last (most recent) slot we have tracked
	slotWatermark int64

	leaderSchedule map[string][]int64

	// for tracking which metrics we have and deleting them accordingly:
	nodekeyTracker *EpochTrackedValidators

	// prometheus:
	TotalTransactionsMetric   prometheus.Gauge
	SlotHeightMetric          prometheus.Gauge
	EpochNumberMetric         prometheus.Gauge
	EpochFirstSlotMetric      prometheus.Gauge
	EpochLastSlotMetric       prometheus.Gauge
	ClusterSlotsByEpochMetric *prometheus.CounterVec
	InflationRewardsMetric    *prometheus.CounterVec
	FeeRewardsMetric          *prometheus.CounterVec
	BlockSizeMetric           *prometheus.GaugeVec
	BlockHeightMetric         prometheus.Gauge
	AssignedLeaderSlotsGauge  prometheus.Gauge

	// New per-epoch gauges
	LeaderSlotsProcessedEpochGauge prometheus.Gauge
	LeaderSlotsSkippedEpochGauge prometheus.Gauge

	processedLeaderSlots map[int64]struct{}
	skippedLeaderSlots map[int64]struct{}
	emittedInflationRewards map[string]struct{} // key: votekey-epoch

	// Leader schedule caching
	cachedLeaderSchedule      map[string][]int64
	cachedLeaderScheduleEpoch int64
}

func SlotWatcherFromConfig(client *rpc.Client, config *ExporterConfig) *SlotWatcher {
	logger := slog.Get()
	watcher := SlotWatcher{
		client:         client,
		logger:         logger,
		config:         config,
		nodekeyTracker: NewEpochTrackedValidators(),
		// metrics:
		TotalTransactionsMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_transactions_total",
			Help: "Total number of transactions processed without error since genesis.",
		}),
		SlotHeightMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_slot_height",
			Help: "The current slot number",
		}),
		EpochNumberMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_epoch_number",
			Help: "The current epoch number.",
		}),
		EpochFirstSlotMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_epoch_first_slot",
			Help: "Current epoch's first slot [inclusive].",
		}),
		EpochLastSlotMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_epoch_last_slot",
			Help: "Current epoch's last slot [inclusive].",
		}),
		ClusterSlotsByEpochMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "solana_cluster_slots_by_epoch_total",
				Help: fmt.Sprintf(
					"Number of slots processed by the cluster, grouped by %s ('%s' or '%s'), and %s",
					SkipStatusLabel, StatusValid, StatusSkipped, EpochLabel,
				),
			},
			[]string{EpochLabel, SkipStatusLabel},
		),
		InflationRewardsMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "solana_validator_inflation_rewards_total",
				Help: fmt.Sprintf("Inflation reward earned, grouped by %s and %s", VotekeyLabel, EpochLabel),
			},
			[]string{VotekeyLabel, EpochLabel},
		),
		FeeRewardsMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "solana_validator_fee_rewards_total",
				Help: fmt.Sprintf("Transaction fee rewards earned, grouped by %s and %s", NodekeyLabel, EpochLabel),
			},
			[]string{NodekeyLabel, EpochLabel},
		),
		BlockSizeMetric: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "solana_validator_block_size",
				Help: fmt.Sprintf("Number of transactions per block, grouped by %s", NodekeyLabel),
			},
			[]string{NodekeyLabel, TransactionTypeLabel},
		),
		BlockHeightMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_node_block_height",
			Help: "The current block height of the node",
		}),
		AssignedLeaderSlotsGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_validator_assigned_leader_slots",
			Help: "Number of leader slots assigned in the schedule for the current epoch for this validator.",
		}),
		LeaderSlotsProcessedEpochGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_validator_leader_slots_processed_epoch",
			Help: "Number of leader slots processed (valid) by this validator in the current epoch.",
		}),
		LeaderSlotsSkippedEpochGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "solana_validator_leader_slots_skipped_epoch",
			Help: "Number of leader slots skipped by this validator in the current epoch.",
		}),
		processedLeaderSlots: make(map[int64]struct{}),
		skippedLeaderSlots: make(map[int64]struct{}),
		emittedInflationRewards: make(map[string]struct{}),
	}
	logger.Info("Registering slot watcher metrics:")
	var collectorsToRegister []prometheus.Collector
	collectorsToRegister = append(collectorsToRegister, 
		watcher.SlotHeightMetric,
		watcher.EpochNumberMetric,
		watcher.TotalTransactionsMetric,
		watcher.EpochFirstSlotMetric,
		watcher.EpochLastSlotMetric,
		watcher.ClusterSlotsByEpochMetric,
		watcher.InflationRewardsMetric,
		watcher.FeeRewardsMetric,
		watcher.BlockSizeMetric,
		watcher.BlockHeightMetric,
	)
	if !config.LightMode {
		collectorsToRegister = append(collectorsToRegister,
			watcher.AssignedLeaderSlotsGauge,
			watcher.LeaderSlotsProcessedEpochGauge,
			watcher.LeaderSlotsSkippedEpochGauge,
		)
	}
	for _, collector := range collectorsToRegister {
		if err := prometheus.Register(collector); err != nil {
			var (
				alreadyRegisteredErr *prometheus.AlreadyRegisteredError
				duplicateErr         = strings.Contains(err.Error(), "duplicate metrics collector registration attempted")
			)
			if errors.As(err, &alreadyRegisteredErr) || duplicateErr {
				continue
			} else {
				logger.Fatal(fmt.Errorf("failed to register collector: %w", err))
			}
		}
	}
	logger.Debugf("Collectors registration complete")
	for _, collector := range collectorsToRegister {
		logger.Debugf("Registered collector type: %T", collector)
	}
	return &watcher
}

func (c *SlotWatcher) WatchSlots(ctx context.Context) {
	ticker := time.NewTicker(c.config.SlotPace)
	defer ticker.Stop()

	c.logger.Infof("Starting slot watcher, running every %vs", c.config.SlotPace.Seconds())

	for {
		select {
		case <-ctx.Done():
			c.logger.Infof("Stopping WatchSlots() at slot %v", c.slotWatermark)
			return
		default:
			<-ticker.C
			// Fetch current slot once per tick
			currentSlot, err := c.client.GetSlot(ctx, rpc.CommitmentFinalized)
			if err != nil {
				c.logger.Errorf("Failed to get current slot: %v", err)
				continue
			}
			// TODO: separate fee-rewards watching from general slot watching, such that general slot watching commitment level can be dropped to confirmed
			commitment := rpc.CommitmentFinalized
			epochInfo, err := c.client.GetEpochInfo(ctx, commitment)
			if err != nil {
				c.logger.Errorf("Failed to get epoch info, bailing out: %v", err)
				continue
			}

			// if we are running for the first time, then we need to set our tracking numbers:
			if c.currentEpoch == 0 {
				c.trackEpoch(ctx, epochInfo)
			}

			c.logger.Infof("Current slot: %v", epochInfo.AbsoluteSlot)
			// These metrics are essential even in light mode
			c.SlotHeightMetric.Set(float64(epochInfo.AbsoluteSlot))
			c.EpochNumberMetric.Set(float64(epochInfo.Epoch))
			
			// In light mode, skip transaction count and block height metrics
			if !c.config.LightMode {
				c.TotalTransactionsMetric.Set(float64(epochInfo.TransactionCount))
				c.BlockHeightMetric.Set(float64(epochInfo.BlockHeight))
			}

			// if we get here, then the tracking numbers are set, so this is a "normal" run.
			// start by checking if we have progressed since last run:
			if epochInfo.AbsoluteSlot <= c.slotWatermark {
				c.logger.Infof("%v slot number has not advanced from %v, skipping", commitment, c.slotWatermark)
				continue
			}

			if epochInfo.Epoch > c.currentEpoch {
				c.closeCurrentEpoch(ctx, epochInfo, currentSlot)
			}

			// update block production metrics up until the current slot:
			// Only move the slot watermark in light mode if we need to for epoch tracking
			if !c.config.LightMode {
				c.moveSlotWatermark(ctx, c.slotWatermark+1, currentSlot)
			}
		}
	}
}

// trackEpoch takes in a new rpc.EpochInfo and sets the SlotWatcher tracking metrics accordingly,
// and updates the prometheus gauges associated with those metrics.
func (c *SlotWatcher) trackEpoch(ctx context.Context, epoch *rpc.EpochInfo) {
	c.logger.Infof("Tracking epoch %v (from %v)", epoch.Epoch, c.currentEpoch)
	firstSlot, lastSlot := GetEpochBounds(epoch)
	if c.currentEpoch == 0 {
		c.currentEpoch = epoch.Epoch
		c.firstSlot = firstSlot
		c.lastSlot = lastSlot
		c.slotWatermark = epoch.AbsoluteSlot - 1
	} else {
		assertf(epoch.Epoch == c.currentEpoch+1, "epoch jumped from %v to %v", c.currentEpoch, epoch.Epoch)
		assertf(firstSlot == c.lastSlot+1, "first slot %v does not follow from current last slot %v", firstSlot, c.lastSlot)
		assertf(c.slotWatermark == c.lastSlot, "can't update epoch when watermark %v hasn't reached current last-slot %v", c.slotWatermark, c.lastSlot)
		c.currentEpoch = epoch.Epoch
		c.firstSlot = firstSlot
		c.lastSlot = lastSlot
	}
	c.logger.Infof("Emitting epoch bounds: %v (slots %v -> %v)", c.currentEpoch, c.firstSlot, c.lastSlot)
	c.EpochNumberMetric.Set(float64(c.currentEpoch))
	if !c.config.LightMode {
		c.EpochFirstSlotMetric.Set(float64(c.firstSlot))
		c.EpochLastSlotMetric.Set(float64(c.lastSlot))
	}
	// update leader schedule only in regular mode
	if !c.config.LightMode {
		c.logger.Infof("Updating leader schedule for epoch %v ...", c.currentEpoch)
		leaderSchedule, err := c.FetchLeaderSchedule(ctx, c.currentEpoch, c.firstSlot)
		if err != nil {
			c.logger.Errorf("Failed to fetch leader schedule, bailing out: %v", err)
		} else {
			c.leaderSchedule = GetTrimmedLeaderScheduleFromCache(leaderSchedule, c.config.NodeKeys)
		}
	}
}

// cleanEpoch deletes old epoch-labelled metrics which are no longer being updated due to an epoch change.
func (c *SlotWatcher) cleanEpoch(ctx context.Context, epoch int64) {
	c.logger.Infof(
		"Waiting %vs before cleaning epoch %d...",
		c.config.EpochCleanupTime.Seconds(), epoch,
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(c.config.EpochCleanupTime):
	}

	c.logger.Infof("Cleaning epoch %d", epoch)
	epochStr := toString(epoch)
	// rewards:
	for i, nodekey := range c.config.NodeKeys {
		c.deleteMetricLabelValues(c.FeeRewardsMetric, "fee-rewards", nodekey, epochStr)
		c.deleteMetricLabelValues(c.InflationRewardsMetric, "inflation-rewards", c.config.VoteKeys[i], epochStr)
	}
	// slots:
	for _, status := range []string{StatusValid, StatusSkipped} {
		c.deleteMetricLabelValues(c.ClusterSlotsByEpochMetric, "cluster-slots-by-epoch", epochStr, status)
	}
	
	c.logger.Infof("Finished cleaning epoch %d", epoch)
}

// closeCurrentEpoch is called when an epoch change-over happens, and we need to make sure we track the last
// remaining slots in the "current" epoch before we start tracking the new one.
func (c *SlotWatcher) closeCurrentEpoch(ctx context.Context, newEpoch *rpc.EpochInfo, currentSlot int64) {
	c.logger.Infof("Closing current epoch %v, moving into epoch %v", c.currentEpoch, newEpoch.Epoch)

	// On epoch transition, reset the per-epoch gauges and slot sets
	c.LeaderSlotsProcessedEpochGauge.Set(0)
	c.LeaderSlotsSkippedEpochGauge.Set(0)
	c.processedLeaderSlots = make(map[int64]struct{})
	c.skippedLeaderSlots = make(map[int64]struct{})

	// In light mode, we skip most of these operations
	if !c.config.LightMode {
		// fetch inflation rewards for epoch we about to close:
		if len(c.config.VoteKeys) > 0 {
			if err := c.fetchAndEmitInflationRewards(ctx, c.currentEpoch); err != nil {
				c.logger.Errorf("Failed to emit inflation rewards, bailing out: %v", err)
			}
		}
		c.moveSlotWatermark(ctx, c.lastSlot, currentSlot)
		go c.cleanEpoch(ctx, c.currentEpoch)
	}
	c.trackEpoch(ctx, newEpoch)
}

// checkValidSlotRange makes sure that the slot range we are going to query is within the current epoch we are tracking.
func (c *SlotWatcher) checkValidSlotRange(from, to int64) error {
	if from < c.firstSlot || to > c.lastSlot {
		return fmt.Errorf(
			"start-end slots (%v -> %v) is not contained within current epoch %v range (%v -> %v)",
			from,
			to,
			c.currentEpoch,
			c.firstSlot,
			c.lastSlot,
		)
	}
	return nil
}

// moveSlotWatermark performs all the slot-watching tasks required to move the slotWatermark to the provided 'to' slot.
func (c *SlotWatcher) moveSlotWatermark(ctx context.Context, to int64, currentSlot int64) {
	c.logger.Infof("Moving watermark %v -> %v", c.slotWatermark, to)
	// Always query the full epoch range for robust metrics
	startSlot := c.firstSlot
	endSlot := c.lastSlot
	c.logger.Debugf("Querying block production for full epoch: [%d -> %d]", startSlot, endSlot)
	blockProduction, err := c.client.GetBlockProduction(ctx, rpc.CommitmentFinalized, startSlot, endSlot)
	if err != nil {
		c.logger.Errorf("Failed to get block production for slots %d-%d: %v", startSlot, endSlot, err)
		return
	}
	c.processLeaderSlotsForValidator(ctx, startSlot, endSlot, currentSlot, blockProduction)
	c.fetchAndEmitBlockInfos(ctx, startSlot, endSlot)
	c.slotWatermark = to
}

// Refactored: processLeaderSlotsForValidator now takes blockProduction as an argument
func (c *SlotWatcher) processLeaderSlotsForValidator(ctx context.Context, startSlot, endSlot, currentSlot int64, blockProduction *rpc.BlockProduction) {
	if c.config.LightMode {
		c.logger.Debug("Skipping leader slot processing in light mode.")
		return
	}
	c.logger.Debugf("Processing leader slots for validator in [%v -> %v]", startSlot, endSlot)
	c.logger.Debugf("Validator identity: %s", c.config.ValidatorIdentity)
	c.logger.Debugf("Block production keys: %v", blockProduction.ByIdentity)
	if endSlot > currentSlot {
		c.logger.Warnf("endSlot %d is greater than currentSlot %d, adjusting endSlot", endSlot, currentSlot)
		endSlot = currentSlot
	}
	if err := c.checkValidSlotRange(startSlot, endSlot); err != nil {
		c.logger.Fatalf("invalid slot range: %v", err)
	}
	validatorNodekey := c.config.ValidatorIdentity
	if validatorNodekey == "" {
		c.logger.Warn("Validator identity not set, cannot process leader slots for validator.")
		return
	}
	// Use the cached leader schedule for this epoch
	leaderSchedule, err := c.FetchLeaderSchedule(ctx, c.currentEpoch, c.firstSlot)
	if err != nil {
		c.logger.Errorf("Failed to fetch leader schedule, bailing out: %v", err)
		return
	}
	leaderSlots := leaderSchedule[validatorNodekey]
	c.logger.Infof("Fetched leaderSlots for validator %s: %v", validatorNodekey, leaderSlots)
	c.logger.Debugf("Number of leader slots for validator %s: %d", validatorNodekey, len(leaderSlots))
	if len(leaderSlots) == 0 {
		c.logger.Warnf("No leader slots for validator %s in [%v -> %v] (expected nonzero if scheduled)", validatorNodekey, startSlot, endSlot)
	}
	c.logger.Infof("Setting AssignedLeaderSlotsGauge to %d (len(leaderSlots)) for validator %s", len(leaderSlots), validatorNodekey)
	c.AssignedLeaderSlotsGauge.Set(float64(len(leaderSlots)))
	prod, ok := blockProduction.ByIdentity[validatorNodekey]
	c.logger.Debugf("Block production for validator %s: %+v (found: %v)", validatorNodekey, prod, ok)
	for _, slot := range leaderSlots {
		if slot > endSlot {
			continue
		}
		if !ok {
			c.logger.Debugf("No block production info for validator %s at slot %d", validatorNodekey, slot)
			continue
		}
		if prod.BlocksProduced > 0 {
			c.processedLeaderSlots[slot] = struct{}{}
		} else {
			c.skippedLeaderSlots[slot] = struct{}{}
		}
	}
	c.LeaderSlotsProcessedEpochGauge.Set(float64(len(c.processedLeaderSlots)))
	c.LeaderSlotsSkippedEpochGauge.Set(float64(len(c.skippedLeaderSlots)))
	c.logger.Infof("Updated per-epoch leader slot gauges: processed=%d, skipped=%d", len(c.processedLeaderSlots), len(c.skippedLeaderSlots))
}

// fetchAndEmitBlockProduction fetches block production from startSlot up to the provided endSlot [inclusive],
// and emits the prometheus metrics,
func (c *SlotWatcher) fetchAndEmitBlockProduction(ctx context.Context, startSlot, endSlot int64) {
	if c.config.LightMode {
		c.logger.Debug("Skipping block-production fetching in light mode.")
		return
	}
	c.logger.Debugf("Fetching block production in [%v -> %v]", startSlot, endSlot)

	// make sure the bounds are contained within the epoch we are currently watching:
	if err := c.checkValidSlotRange(startSlot, endSlot); err != nil {
		c.logger.Fatalf("invalid slot range: %v", err)
	}

	// fetch block production:
	blockProduction, err := c.client.GetBlockProduction(ctx, rpc.CommitmentFinalized, startSlot, endSlot)
	if err != nil {
		c.logger.Errorf("Failed to get block production, bailing out: %v", err)
		return
	}

	// emit the metrics:
	var (
		epochStr = toString(c.currentEpoch)
		nodekeys []string
	)
	for address, production := range blockProduction.ByIdentity {
		valid := float64(production.BlocksProduced)
		skipped := float64(production.LeaderSlots - production.BlocksProduced)

		if slices.Contains(c.config.NodeKeys, address) || c.config.ComprehensiveSlotTracking {
			nodekeys = append(nodekeys, address)
		}

		// additionally, track block production for the whole cluster:
		c.ClusterSlotsByEpochMetric.WithLabelValues(epochStr, StatusValid).Add(valid)
		c.ClusterSlotsByEpochMetric.WithLabelValues(epochStr, StatusSkipped).Add(skipped)
	}

	// update tracked nodekeys:
	c.nodekeyTracker.AddTrackedNodekeys(c.currentEpoch, nodekeys)

	c.logger.Debugf("Fetched block production in [%v -> %v]", startSlot, endSlot)
}

// fetchAndEmitBlockInfos fetches and emits all the fee rewards (+ block sizes) for the tracked addresses between the
// startSlot and endSlot [inclusive]
func (c *SlotWatcher) fetchAndEmitBlockInfos(ctx context.Context, startSlot, endSlot int64) {
	if c.config.LightMode {
		c.logger.Debug("Skipping block-infos fetching in light mode.")
		return
	}
	c.logger.Debugf("Fetching fee rewards in [%v -> %v]", startSlot, endSlot)

	if err := c.checkValidSlotRange(startSlot, endSlot); err != nil {
		c.logger.Fatalf("invalid slot range: %v", err)
	}
	scheduleToFetch := SelectFromSchedule(c.leaderSchedule, startSlot, endSlot)
	for nodekey, leaderSlots := range scheduleToFetch {
		if len(leaderSlots) == 0 {
			continue
		}

		c.logger.Infof("Fetching fee rewards for %v in [%v -> %v]: %v ...", nodekey, startSlot, endSlot, leaderSlots)
		for _, slot := range leaderSlots {
			err := c.fetchAndEmitSingleBlockInfo(ctx, nodekey, c.currentEpoch, slot)
			if err != nil {
				c.logger.Errorf("Failed to fetch fee rewards for %v at %v: %v", nodekey, slot, err)
			}
		}
	}

	c.logger.Debugf("Fetched fee rewards in [%v -> %v]", startSlot, endSlot)
}

// fetchAndEmitSingleBlockInfo fetches and emits the fee reward + block size for a single block.
func (c *SlotWatcher) fetchAndEmitSingleBlockInfo(
	ctx context.Context, nodekey string, epoch int64, slot int64,
) error {
	transactionDetails := "none"
	if c.config.MonitorBlockSizes {
		transactionDetails = "full"
	}
	block, err := c.client.GetBlock(ctx, rpc.CommitmentConfirmed, slot, transactionDetails)
	if err != nil {
		var rpcError *rpc.Error
		if errors.As(err, &rpcError) {
			// this is the error code for slot was skipped:
			if rpcError.Code == rpc.SlotSkippedCode && strings.Contains(rpcError.Message, "skipped") {
				c.logger.Infof("slot %v was skipped, no fee rewards.", slot)
				return nil
			}
		}
		return err
	}

	foundFeeReward := false
	for _, reward := range block.Rewards {
		if strings.ToLower(reward.RewardType) == "fee" {
			// make sure we haven't made a logic issue or something:
			assertf(
				reward.Pubkey == nodekey,
				"fetching fee reward for %v but got fee reward for %v",
				nodekey,
				reward.Pubkey,
			)
			amount := float64(reward.Lamports) / rpc.LamportsInSol
			c.FeeRewardsMetric.WithLabelValues(nodekey, toString(epoch)).Add(amount)
			foundFeeReward = true
		}
	}

	if !foundFeeReward {
		c.logger.Errorf("No fee reward for slot %d", slot)
	}

	// track block size:
	if c.config.MonitorBlockSizes {
		// now count and emit votes:
		voteCount, err := CountVoteTransactions(block)
		if err != nil {
			return err
		}
		c.BlockSizeMetric.WithLabelValues(nodekey, TransactionTypeVote).Set(float64(voteCount))
		nonVoteCount := len(block.Transactions) - voteCount
		c.BlockSizeMetric.WithLabelValues(nodekey, TransactionTypeNonVote).Set(float64(nonVoteCount))
	}
	return nil
}

// fetchAndEmitInflationRewards fetches and emits the inflation rewards for the configured inflationRewardAddresses
// at the provided epoch
func (c *SlotWatcher) fetchAndEmitInflationRewards(ctx context.Context, epoch int64) error {
	if c.config.LightMode {
		c.logger.Debug("Skipping inflation-rewards fetching in light mode.")
		return nil
	}

	c.logger.Infof("Fetching inflation reward for epoch %v ...", toString(epoch))
	rewardInfos, err := c.client.GetInflationReward(ctx, rpc.CommitmentConfirmed, c.config.VoteKeys, epoch)
	if err != nil {
		return fmt.Errorf("error fetching inflation rewards: %w", err)
	}

	for i, rewardInfo := range rewardInfos {
		if i >= len(c.config.VoteKeys) {
			c.logger.Debugf("Array index out of bounds! i=%d, VoteKeys length=%d", i, len(c.config.VoteKeys))
			continue
		}
		address := c.config.VoteKeys[i]
		if rewardInfo.Amount == 0 && rewardInfo.Epoch == 0 {
			c.logger.Debugf("Reward info is zero value for address %s at index %d", address, i)
			continue
		}
		reward := float64(rewardInfo.Amount) / rpc.LamportsInSol
		c.logger.Debugf("About to add reward %f SOL for address %s in epoch %s", reward, address, toString(epoch))
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Errorf("PANIC in metric emission: %v", r)
				}
			}()
			c.InflationRewardsMetric.WithLabelValues(address, toString(epoch)).Add(reward)
		}()
		c.logger.Debugf("Added reward metric with labels address=%s, epoch=%s", address, toString(epoch))
	}
	c.logger.Infof("Fetched inflation reward for epoch %v.", epoch)
	return nil
}

func (c *SlotWatcher) deleteMetricLabelValues(metric *prometheus.CounterVec, name string, lvs ...string) {
	c.logger.Debugf("deleting %v with lv %v", name, lvs)
	if ok := metric.DeleteLabelValues(lvs...); !ok {
		c.logger.Errorf("Failed to delete %s with label values %v", name, lvs)
	}
}

// FetchLeaderSchedule fetches the leader schedule for the current epoch, using a cache to avoid redundant RPC calls.
func (c *SlotWatcher) FetchLeaderSchedule(ctx context.Context, currentEpoch int64, epochFirstSlot int64) (map[string][]int64, error) {
	if c.cachedLeaderSchedule != nil && c.cachedLeaderScheduleEpoch == currentEpoch {
		c.logger.Debugf("Using cached leader schedule for epoch %d", currentEpoch)
		return c.cachedLeaderSchedule, nil
	}
	leaderSchedule, err := c.client.GetLeaderSchedule(ctx, rpc.CommitmentFinalized, epochFirstSlot)
	if err != nil {
		return nil, err
	}
	c.cachedLeaderSchedule = leaderSchedule
	c.cachedLeaderScheduleEpoch = currentEpoch
	c.logger.Infof("Fetched and cached new leader schedule for epoch %d", currentEpoch)
	return leaderSchedule, nil
}

// Helper to trim the cached leader schedule for specific node keys
func GetTrimmedLeaderScheduleFromCache(schedule map[string][]int64, nodeKeys []string) map[string][]int64 {
	trimmed := make(map[string][]int64)
	if schedule == nil {
		return trimmed
	}
	for _, key := range nodeKeys {
		if slots, ok := schedule[key]; ok {
			trimmed[key] = slots
		}
	}
	return trimmed
}
