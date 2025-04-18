package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/asymmetric-research/solana-exporter/pkg/rpc"
	"github.com/asymmetric-research/solana-exporter/pkg/slog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	slog.Init()
	logger := slog.Get()
	ctx := context.Background()

	validatorIdentity := flag.String("validator-identity", "", "Validator identity to monitor")
	voteAccountPubkey := flag.String("vote-account-pubkey", "", "Vote account public key to monitor")

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

	rpcClient := rpc.NewRPCClient(config.RpcUrl, config.HttpTimeout)
	collector := NewSolanaCollector(rpcClient, config)
	slotWatcher := NewSlotWatcher(rpcClient, config)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go slotWatcher.WatchSlots(ctx)

	prometheus.MustRegister(collector)
	http.Handle("/metrics", promhttp.Handler())

	logger.Infof("listening on %s", config.ListenAddress)
	logger.Fatal(http.ListenAndServe(config.ListenAddress, nil))
}
