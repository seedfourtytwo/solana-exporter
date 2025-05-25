package main

import (
	"context"
	"net/http"
	"time"

	"github.com/seedfourtytwo/solana-exporter/pkg/rpc"
	"github.com/seedfourtytwo/solana-exporter/pkg/slog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// BuildVersion is set at build time using -ldflags
var BuildVersion = "dev"

func main() {
	slog.Init()
	logger := slog.Get()
	logger.Infof("DEBUG: solana-exporter build version: %s", BuildVersion)
	logger.Infof("DEBUG: main() started")
	ctx := context.Background()

	config, err := NewExporterConfigFromCLI(ctx)
	if err != nil {
		logger.Fatal(err)
	}
	if config.ComprehensiveSlotTracking {
		logger.Warn(
			"Comprehensive slot tracking will lead to potentially thousands of new " +
				"Prometheus metrics being created every epoch.",
		)
	}

	logger.Infof("DEBUG: VoteKeys at startup: %v", config.VoteKeys)

	rpcClient := rpc.NewRPCClient(config.RpcUrl, config.HttpTimeout)
	collector := NewSolanaCollector(rpcClient, config)
	slotWatcher := SlotWatcherFromConfig(rpcClient, config)

	// Fetch and emit inflation rewards for the last 3 epochs (if available) instead of just the previous epoch
	epochInfo, err := rpcClient.GetEpochInfo(ctx, rpc.CommitmentFinalized)
	if err != nil {
		logger.Errorf("Failed to fetch epoch info on startup: %v", err)
	} else {
		for i := 1; i <= 3; i++ {
			if epochInfo.Epoch >= int64(i) {
				if err := slotWatcher.fetchAndEmitInflationRewards(ctx, epochInfo.Epoch-int64(i)); err != nil {
					logger.Errorf("Failed to emit inflation rewards for epoch %d on startup: %v", epochInfo.Epoch-int64(i), err)
				}
			}
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go slotWatcher.WatchSlots(ctx)
	
	// Start fast metrics collection if configured
	if config.FastMetricsInterval > 0 {
		logger.Infof("Starting fast metrics collection with interval: %v", config.FastMetricsInterval)
		collector.StartFastMetricsCollection(config.FastMetricsInterval)
		
		// Let the fast collection process start up
		time.Sleep(500 * time.Millisecond)
		
		defer collector.StopFastMetricsCollection()
	}

	prometheus.MustRegister(collector)
	http.Handle("/metrics", promhttp.Handler())

	logger.Infof("listening on %s", config.ListenAddress)
	logger.Fatal(http.ListenAndServe(config.ListenAddress, nil))
}
