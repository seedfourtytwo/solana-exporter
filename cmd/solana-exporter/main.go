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

func main() {
	slog.Init()
	logger := slog.Get()
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
	slotWatcher := NewSlotWatcher(rpcClient, config)
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
