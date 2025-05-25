# Solana Exporter: RPC Method Usage & Optimization

## Table of Contents
- [Optimization Summary Table](#optimization-summary-table)
- [Prometheus RPC Call Monitoring](#prometheus-rpc-call-monitoring)
- [Method Details & Optimizations](#method-details--optimizations)
- [Notes](#notes)
- [General Optimization Strategies](#general-optimization-strategies)

This document explains the purpose, usage, and optimization opportunities for each Solana RPC method used in your exporter. **Last updated: after major deduplication and caching improvements (May 2025)**

## Prometheus RPC Call Monitoring
All RPC calls made by the exporter are now tracked in Prometheus with their own metrics. The primary metric is `solana_rpc_call_count`, which records the number of calls per RPC method. Additional metrics such as `solana_rpc_call_duration_seconds` and `solana_rpc_call_errors_total` provide insight into latency and errors. This allows you to directly monitor, visualize, and alert on the frequency, latency, and errors of each RPC method. This provides full transparency into exporter RPC usage and helps with troubleshooting and optimization.
---

## Method Details & Optimizations

### getVoteAccounts
- **Purpose:** Returns info and stake for all voting accounts.
- **Optimization:** Fetched once per scrape, reused for all metrics. Only emits metrics for your validator (unless cluster-wide tracking is enabled).
- **Rationale:** Multiple metrics need this data, but the set of vote accounts can change frequently. Deduplicating within a scrape avoids redundant calls while keeping metrics fresh.

### getSlot
- **Purpose:** Returns the current slot at a given commitment level.
- **Optimization:** Fetched once per scrape/tick, reused for all slot-dependent logic.
- **Rationale:** Slot changes rapidly, but all metrics in a scrape/tick can use the same value. Deduplication avoids redundant calls.

### getBalance
- **Purpose:** Returns the SOL balance for an account.
- **Optimization:** Now cached for 1 minute per address. All scrapes within 1 minute use the cached value; after 1 minute, a fresh value is fetched for each address.
- **Rationale:** SOL balances do not change rapidly for most use cases. Caching for 1 minute dramatically reduces RPC calls (by up to 90% or more) while keeping metrics sufficiently fresh for alerting and dashboards. This allows you to keep a fast scrape interval for other metrics without incurring high API usage for balances.

### getBlockProduction
- **Purpose:** Returns recent block production info.
- **Optimization:** Fetched once per tick, reused for all block production metrics.
- **Rationale:** Block production is updated per tick, so one call suffices for all related metrics.

### getLeaderSchedule
- **Purpose:** Returns the leader schedule for an epoch.
- **Optimization:** Now cached for the duration of the epoch. Only fetched on epoch change or exporter restart. Calls dropped to near zero except at epoch change.
- **Rationale:** The leader schedule is fixed for the epoch and only changes at epoch boundaries. Caching eliminates redundant calls and saves API credits.

### getEpochInfo
- **Purpose:** Returns info about the current epoch.
- **Optimization:** Now globally cached for 15s across the exporter. All users share the same cache, halving (or better) the number of calls.
- **Rationale:** Epoch info changes slowly (every few seconds/slots). A short cache window reduces redundant calls from different parts of the exporter.

### minimumLedgerSlot
- **Purpose:** Returns the lowest slot the node has in its ledger.
- **Optimization:** Now cached for 10 minutes. Only fetches a new value if the cache is expired.
- **Rationale:** This value changes very slowly (only when the node purges old data). Polling every scrape is unnecessary; a 10-minute cache dramatically reduces calls with no loss of monitoring accuracy.

### getFirstAvailableBlock
- **Purpose:** Returns the slot of the lowest confirmed block not purged from the ledger.
- **Optimization:** Now cached for 10 minutes. Only fetches a new value if the cache is expired.
- **Rationale:** This value changes very slowly (only when the node purges old blocks). Polling every scrape is unnecessary; a 10-minute cache dramatically reduces calls with no loss of monitoring accuracy.

### getHealth
- **Purpose:** Checks if the node is healthy (liveness/readiness).
- **Optimization:** Called once per scrape. Already optimal for monitoring.
- **Rationale:** Health checks are lightweight and should be frequent for timely alerting.

### getVersion
- **Purpose:** Returns the Solana version running on the node.
- **Optimization:** Cached for 1 hour. Only fetched on exporter restart or after 1 hour.
- **Rationale:** Node version rarely changes. Caching avoids thousands of unnecessary calls per day.

### getIdentity
- **Purpose:** Returns the node's identity pubkey.
- **Optimization:** Cached for the duration of the epoch. Only fetched on epoch change or exporter restart.
- **Rationale:** Node identity almost never changes except on restart. Caching per epoch is safe and efficient.

### getInflationReward
- **Purpose:** Returns inflation/staking rewards for addresses for a given epoch.
- **Optimization:** Now only fetched once per epoch, immediately after an epoch change.
- **Rationale:** Rewards are only available for completed epochs. Fetching after each epoch transition ensures fresh data with minimal RPC usage.

---

## Notes
- **Call rates** are approximate and depend on your scrape/tick intervals and number of tracked addresses.
- **After these optimizations, your exporter is highly efficient and cost-effective for RPC usage.**
- For further reductions, consider increasing your scrape interval, but be aware this may reduce metric freshness. **With 1-minute balance caching, you can keep a fast scrape interval for all other metrics.**

---

## General Optimization Strategies
- **Deduplicate**: Ensure each RPC call is made only once per scrape and reused for all relevant metrics.
- **Cache**: Cache values that rarely change (e.g., version, identity, leader schedule).
- **Reduce Scrape Frequency**: For non-critical metrics, scrape less often (e.g., every 1-5 minutes).
- **Light Mode**: Use light mode for node-specific metrics only.
- **Batch/Parallelize**: Where possible, batch or parallelize requests for efficiency.

---