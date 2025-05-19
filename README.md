# Solana Exporter
## Overview

The Solana Exporter exports basic monitoring data from a Solana node, using the 
[Solana RPC API](https://solana.com/docs/rpc).

## Changes I made so far from the original project https://github.com/asymmetric-research/solana-exporter

### Validator Credits Tracking (2024)
Added comprehensive validator credits tracking with the following improvements:

1. **New Metrics**:
   - `solana_validator_current_epoch_credits`: Tracks credits earned in the current epoch
   - `solana_validator_total_credits`: Tracks total accumulated credits since genesis

2. **Configuration Changes**:
   - Replaced `-nodekey` with `-validator-identity` for clearer parameter naming
   - Added `-vote-account-pubkey` parameter for precise vote account tracking
   - Updated systemd service configuration for better reliability

3. **Accuracy Improvements**:
   - Fixed credit calculation to accurately reflect current epoch earnings
   - Added proper labeling with validator identity
   - Implemented real-time credit tracking with minimal latency

4. **Example Service Configuration**:
```bash
[Unit]
Description=Solana Exporter for Prometheus
After=network.target

[Service]
User=sol
WorkingDirectory=/home/sol
ExecStart=/home/sol/validators/monitoring/solana-exporter/solana-exporter \
    -rpc-url http://127.0.0.1:8899 \
    -listen-address 0.0.0.0:9100 \
    -validator-identity <VALIDATOR_IDENTITY> \
    -vote-account-pubkey <VOTE_ACCOUNT_PUBKEY>
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

5. **Grafana Integration**:
   - Added support for real-time credit monitoring
   - Recommended queries for tracking current epoch and total credits
   - Support for credit earning rate calculations

### Example Usage

To use the Solana Exporter, simply run the program with the desired 
[command line configuration](#Command-Line-Arguments), e.g.,

```shell
solana-exporter \
  -nodekey <VALIDATOR_IDENTITY_1> -nodekey <VALIDATOR_IDENTITY_2> \
  -balance-address <ADDRESS_1> -balance-address <ADDRESS_2> \
  -comprehensive-slot-tracking \
  -monitor-block-sizes
  -active-identity <MY_ACTIVE_IDENTITY>
```

![Solana Exporter Dashboard Sample](assets/solana-dashboard-screenshot.png)

### Features
#### Balance Tracking

Using the `-balance-address <ADDRESS>` configuration parameter, the exporter can be used to monitor any account's
SOL balance. This parameter can be set multiple times to track multiple addresses:

```shell
solana-exporter \
  -balance-address <ADDRESS_1> \
  -balance-address <ADDRESS_2> \
  -balance-address <ADDRESS_3>
```

The exporter automatically tracks balances for:
1. All specified `-balance-address` values
2. All configured validator identity keys (via `-nodekey`)
3. All corresponding vote account keys
4. The validator identity (if specified with `-validator-identity`)
5. The vote account (if specified with `-vote-account-pubkey`)

##### Querying Balance Metrics

To view an address's balance in Prometheus or Grafana, use the query:

```
solana_account_balance{address="YourSolanaAddressHere"}
```

For example:
```
solana_account_balance{address="JDa72CkixfF1JD9aYZosWqXyFCZwMpnVjR15bVBW2QRF"}  # Identity address
solana_account_balance{address="3TEX5gBjcZCzAz3AYT2BQrwpDTSUd5FtszPs7yx9iGGL"}   # Vote account
```

Note that addresses must be configured in the exporter startup parameters before they can be queried.

#### Block Sizes

If the `-monitor-block-sizes` flag is set, then the exporter will export the number of transactions (both vote-only and 
non-vote transactions) in blocks produced by the monitored validators. This is a critical validator performance metric. 

Cluster average block size can be inferred by dividing total network transactions by total block height.

#### Income Reporting

The exporter exports metrics regarding total priority fee revenue and inflation reward revenue earned by the 
monitored validators.

#### Skip Rate

The exporter does not directly export skip rate, as this needs to be defined as an average over a desired timeframe. 
However, the exporter does track the monitored validators leader slots and whether they are `valid` or `skipped`.

The example prometheus setup contains [recording rules](prometheus/solana-rules.yml) for measuring average skip rate 
for both individual validators and a cluster-level over hourly, daily and epoch intervals.

#### Active/Passive Monitoring

The `solana_node_is_active` metric simply reports whether the node (on which the exporter is running) has the same 
identity-keypair as that configured with the `-active-identity` flag. The `-active-identity` flag should be used to 
specify the primary identity when using a 
[non-delinquent backup validator](https://pumpkins-pool.gitbook.io/pumpkins-pool).

#### Light Mode

Certain metrics, such as validator leader slots, income, block size and active stake, are visible on-chain through any 
trusted node. However, other metrics such as node health and block height can only be viewed from an exporter running 
on the node in question. Thus, on a node in which fine margins of performance are of critical interest, the exporter 
can be set to `-light-mode`. In light mode, it will only export metrics that cannot be viewed from other nodes.

This is particularly useful in setups that contain an important validator and utility RPC node - the exporter can be 
run in light mode on the validator and in full capacity on the RPC node (configured to monitor the validator through 
use of the `-nodekey` parameter).

#### General Performance and Health

In addition to the above features, the exporter provides key metrics for monitoring Solana node health and performance. 
See [Metrics](#metrics) below for more details.

## Light Mode Enhancements

### Improved Light Mode to Minimize Validator Resource Usage

The exporter's `-light-mode` flag has been significantly enhanced to eliminate all metric overlap between light mode and regular mode. This reduces the resource demands on validators by eliminating duplicate metrics that can be obtained from any RPC node.

#### Key Light Mode Improvements:

1. **Complete Elimination of Cluster Metrics**: Light mode now completely excludes all `solana_cluster_*` metrics, which can be obtained from any RPC node.

2. **Node-Specific Only**: In light mode, only metrics specific to the node itself are collected:
   - `solana_node_*` metrics (health, slot height, epoch number, etc.)
   - When using validator identity parameters: validator credits metrics

3. **Compatible with Validator Credits**: Light mode works with `-validator-identity` and `-vote-account-pubkey` parameters to track validator credits while still minimizing resource usage.

#### Available Metrics in Light Mode:

| Metric                             | Description                                                        |
|------------------------------------|--------------------------------------------------------------------|
| solana_node_epoch_number           | The current epoch number                                           |
| solana_node_first_available_block  | Lowest confirmed block not purged from ledger                      |
| solana_node_identity               | Node identity                                                      |
| solana_node_is_healthy             | Node health status                                                 |
| solana_node_minimum_ledger_slot    | Lowest slot in the node's ledger                                   |
| solana_node_num_slots_behind       | Slots behind the latest cluster slot                               |
| solana_node_slot_height            | Current slot number                                                |
| solana_validator_current_epoch_credits* | Current epoch credits (with validator identity params)        |
| solana_validator_total_credits*    | Total accumulated credits (with validator identity params)         |

*Only available when using `-validator-identity` and `-vote-account-pubkey`

#### Example Light Mode Usage:

```bash
solana-exporter \
  -rpc-url http://localhost:8899 \
  -light-mode \
  -validator-identity <VALIDATOR_IDENTITY> \
  -vote-account-pubkey <VOTE_ACCOUNT_PUBKEY> \
  -fast-metrics-interval 3
```

This configuration provides essential validator monitoring with minimal RPC load.

#### Example Systemd Service with Fast Metrics:

```
[Unit]
Description=Solana Exporter for Prometheus
After=network.target

[Service]
User=sol
WorkingDirectory=/home/sol
ExecStart=/home/sol/validators/monitoring/solana-exporter/solana-exporter \
    -rpc-url http://127.0.0.1:8899 \
    -listen-address 0.0.0.0:9100 \
    -validator-identity <VALIDATOR_IDENTITY> \
    -vote-account-pubkey <VOTE_ACCOUNT_PUBKEY> \
    -light-mode \
    -fast-metrics-interval 3
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

## Installation
### Build

Assuming you already have [Go installed](https://go.dev/doc/install), the `solana-exporter` can be installed by 
cloning this repository and building the binary:

```shell
git clone https://github.com/asymmetric-research/solana-exporter.git
cd solana-exporter
CGO_ENABLED=0 go build ./cmd/solana-exporter
```

## Configuration
### Command Line Arguments

The exporter is configured via the following command line arguments:

| Option                                 | Description                                                                                                                                                                                                             | Default                   |
|----------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---------------------------|
| `-balance-address`                     | Address to monitor SOL balances for, in addition to the identity and vote accounts of the provided nodekeys - can be set multiple times.                                                                                | N/A                       |
| `-comprehensive-slot-tracking`         | Set this flag to track `solana_leader_slots_by_epoch` for all validators.                                                                                                                                               | `false`                   |
| `-comprehensive-vote-account-tracking` | Set this flag to track vote-account metrics for all validators.                                                                                                                                                         | `false`                   |
| `-fast-metrics-interval <SECONDS>`     | Collection interval in seconds **exclusively** for vote distance and root distance metrics. All other metrics use the standard Prometheus scrape interval (typically 15 seconds). Must provide a numeric value (e.g., `-fast-metrics-interval 3`).        | `3`                       |
| `-http-timeout`                        | HTTP timeout to use, in seconds.                                                                                                                                                                                        | `60`                      |
| `-light-mode`                          | Set this flag to enable light-mode. In light mode, only metrics unique to the node being queried are reported (i.e., metrics such as `solana_inflation_rewards` which are visible from any RPC node, are not reported). | `false`                   |
| `-listen-address`                      | Prometheus listen address.                                                                                                                                                                                              | `":8080"`                 |
| `-monitor-block-sizes`                 | Set this flag to track block sizes (number of transactions) for the configured validators.                                                                                                                              | `false`                   |
| `-nodekey`                             | Solana nodekey (identity account) representing a validator to monitor - can set multiple times.                                                                                                                         | N/A                       |
| `-rpc-url`                             | Solana RPC URL (including protocol and path), e.g., `"http://localhost:8899"` or `"https://api.mainnet-beta.solana.com"`                                                                                                | `"http://localhost:8899"` |
| `-slot-pace`                           | This is the time (in seconds) between slot-watching metric collections                                                                                                                                                  | `1`                       |
| `-active-identity`                     | Validator identity public key used to determine if the node is considered active in the `solana_node_is_active` metric.                                                                                                 | N/A                       |
| `-epoch-cleanup-time`                  | The time to wait before cleaning old epoch metrics from the prometheus endpoint.                                                                                                                                        |                           |
| `-validator-identity`                  | Validator identity public key for tracking validator-specific metrics.                                                                                                                                                  | N/A                       |
| `-vote-account-pubkey`                 | Vote account public key to monitor. If not provided but validator-identity is, the exporter will attempt to find it.                                                                                                    | N/A                       |

### Notes on Configuration

* `-light-mode` is incompatible with `-nodekey`, `-balance-address`, `-monitor-block-sizes`, and 
`-comprehensive-slot-tracking`, as these options control metrics which are not monitored in `-light-mode`.
* ***WARNING***:
  * Configuring `-comprehensive-slot-tracking` will lead to potentially thousands of new Prometheus metrics being 
  created every epoch.
  * Configuring `-monitor-block-sizes` with many `-nodekey`'s can potentially strain the node - every block produced 
  by a configured `-nodekey` is fetched, and a typical block can be as large as 5MB.

## Metrics
### Overview

The tables below describe all the metrics collected by the `solana-exporter`:

| Metric                                         | Description                                                                                                           | Labels                        | Mode  |
|------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------|-------------------------------|-------|
| `solana_validator_active_stake`                | Active stake (in SOL) per validator.                                                                                  | `votekey`, `nodekey`          | Full  |
| `solana_cluster_active_stake`                  | Total active stake (in SOL) of the cluster.                                                                           | N/A                           | Full  |
| `solana_validator_last_vote`                   | Last voted-on slot per validator.                                                                                     | `votekey`, `nodekey`          | Full  |
| `solana_cluster_last_vote`                     | Most recent voted-on slot of the cluster.                                                                             | N/A                           | Full  |
| `solana_validator_root_slot`                   | Root slot per validator.                                                                                              | `votekey`, `nodekey`          | Full  |
| `solana_cluster_root_slot`                     | Max root slot of the cluster.                                                                                         | N/A                           | Full  |
| `solana_validator_delinquent`                  | Whether a validator is delinquent.                                                                                    | `votekey`, `nodekey`          | Full  |
| `solana_cluster_validator_count`               | Total number of validators in the cluster.                                                                            | `state`                       | Full  |
| `solana_account_balance`                       | Solana account balances.                                                                                              | `address`                     | Both  |
| `solana_node_version`                          | Node version of solana.                                                                                               | `version`                     | Both  |
| `solana_node_is_healthy`                       | Whether the node is healthy.                                                                                          | N/A                           | Both  |
| `solana_node_num_slots_behind`                 | The number of slots that the node is behind the latest cluster confirmed slot.                                        | N/A                           | Both  |
| `solana_node_minimum_ledger_slot`              | The lowest slot that the node has information about in its ledger.                                                    | N/A                           | Both  |
| `solana_node_first_available_block`            | The slot of the lowest confirmed block that has not been purged from the node's ledger.                               | N/A                           | Both  |
| `solana_node_transactions_total`               | Total number of transactions processed without error since genesis.                                                   | N/A                           | Both  |
| `solana_node_slot_height`                      | The current slot number.                                                                                              | N/A                           | Both  |
| `solana_node_epoch_number`                     | The current epoch number.                                                                                             | N/A                           | Both  |
| `solana_node_epoch_first_slot`                 | Current epoch's first slot [inclusive].                                                                               | N/A                           | Full  |
| `solana_node_epoch_last_slot`                  | Current epoch's last slot [inclusive].                                                                                | N/A                           | Full  |
| `solana_validator_leader_slots_total`          | Number of slots processed.                                                                                            | `status`, `nodekey`           | Full  |
| `solana_validator_leader_slots_by_epoch_total` | Number of slots processed per validator.                                                                              | `status`, `nodekey`, `epoch`  | Full  |
| `solana_cluster_slots_by_epoch_total`          | Number of slots processed by the cluster.                                                                             | `status`, `epoch`             | Full  |
| `solana_validator_inflation_rewards`           | Inflation reward earned.                                                                                              | `votekey`, `epoch`            | Full  |
| `solana_validator_fee_rewards`                 | Transaction fee rewards earned.                                                                                       | `nodekey`, `epoch`            | Full  |
| `solana_validator_block_size`                  | Number of transactions per block.                                                                                     | `nodekey`, `transaction_type` | Full  |
| `solana_node_block_height`                     | The current block height of the node.                                                                                 | N/A                           | Both  |
| `solana_node_is_active`                        | Whether the node is active and participating in consensus.                                                            | `identity`                    | Both  |
| `solana_validator_commission`                  | Validator commission percentage rate (0-100).                                                                         | `nodekey`                     | Full  |
| `solana_validator_current_epoch_credits`       | Current epoch credits for the validator.                                                                              | `nodekey`                     | Both* |
| `solana_validator_total_credits`               | Total accumulated credits for the validator since genesis.                                                            | `nodekey`                     | Both* |
| `solana_validator_vote_distance`               | Gap between current slot and last vote (lower is better).                                                             | `identity`                    | Both  |
| `solana_validator_root_distance`               | Gap between last vote and root slot (tower stability metric).                                                         | `identity`                    | Both  |
| `solana_validator_assigned_leader_slots`       | Number of leader slots assigned in the schedule for the current epoch for this validator.                             | N/A                           | Full  |
| `solana_validator_leader_slots_processed_epoch`| Number of leader slots processed (valid) by this validator in the current epoch.                                      | N/A                           | Full  |
| `solana_validator_leader_slots_skipped_epoch`  | Number of leader slots skipped by this validator in the current epoch.                                                | N/A                           | Full  |

*Only available in light mode when using `-validator-identity` and `-vote-account-pubkey`

### Validator Performance Metrics

The new validator performance metrics provide critical insights into validator voting behavior:

#### Vote Distance

The `solana_validator_vote_distance` metric measures the gap between the current slot and the last vote submitted by your validator. 
This metric indicates how responsive your validator is in voting on new slots.

**Calculation**: `current_slot - last_vote_slot`

- **Lower values are better**: Ideally, this value should be small (1-5 slots)
- **High values indicate**: Network issues, validator falling behind, or performance problems
- **Collection frequency**: Collected at the interval specified by `-fast-metrics-interval` (default: 3 seconds)

**Interpreting Vote Distance**:
- **1-5 slots**: Optimal performance - your validator is voting promptly
- **6-20 slots**: Minor lag - still functional but might indicate network or resource issues
- **>20 slots**: Significant lag - validator may be having problems keeping up with the network

#### Root Distance

The `solana_validator_root_distance` metric tracks the gap between the last vote and the root slot of the validator.
This metric provides insight into tower stability and how quickly votes are being finalized.

**Calculation**: `last_vote_slot - root_slot`

- **Normal range**: ~20-50 slots (depends on current network confirmation times)
- **Sudden increases**: May indicate consensus issues or network problems
- **Collection frequency**: Collected at the interval specified by `-fast-metrics-interval` (default: 3 seconds)

**Interpreting Root Distance**:
- Root distance typically maintains a relatively consistent pattern based on network-wide confirmation rates
- A growing root distance (increasing over time) may indicate the validator's votes aren't being included in consensus
- A very small root distance could indicate the validator just restarted or had a tower rebuild

**Note**: The `-fast-metrics-interval` flag **only** affects these two metrics. All other metrics continue to be collected on the standard Prometheus scrape interval (typically 15 seconds). This ensures you get high-frequency data for these critical metrics without increasing the load on your validator from other metric collections.

### Labels

The table below describes the various metric labels:

| Label              | Description                                   | Options / Example                                    | 
|--------------------|-----------------------------------------------|------------------------------------------------------|
| `nodekey`          | Validator identity account address.           | e.g, `Certusm1sa411sMpV9FPqU5dXAYhmmhygvxJ23S6hJ24`  | 
| `votekey`          | Validator vote account address.               | e.g., `CertusDeBmqN8ZawdkxK5kFGMwBXdudvWHYwtNgNhvLu` |
| `address`          | Solana account address.                       | e.g., `Certusm1sa411sMpV9FPqU5dXAYhmmhygvxJ23S6hJ24` |
| `version`          | Solana node version.                          | e.g., `v1.18.23`                                     |
| `state`            | Whether a validator is current or delinquent. | `current`, `delinquent`                              |
| `status`           | Whether a slot was skipped or valid.          | `valid`, `skipped`                                   |
| `epoch`            | Solana epoch number.                          | e.g., `663`                                          |
| `transaction_type` | General transaction type.                     | `vote`, `non_vote`                                   |

## Quick Start Example

```bash
solana-exporter \
  -rpc-url <YOUR_RPC_URL_HERE> \
  -listen-address 0.0.0.0:9101 \
  -validator-identity <VALIDATOR_IDENTITY> \
  -vote-account-pubkey <VOTE_ACCOUNT_PUBKEY> \
  -slot-pace 15
```

> **WARNING:**
> - Always set your own `-rpc-url` (do **not** use a public or default endpoint in production).
> - Never commit or share your private RPC endpoint in public repositories or forums.
> - If you suspect your endpoint is leaked, rotate it immediately.

## New Features & Changes (2024)

### 1. **Default Slot Pace is Now 15 Seconds**
- The exporter collects slot metrics every 15 seconds by default (`-slot-pace 15`).
- This reduces RPC and credit usage and matches typical Prometheus scrape intervals.

### 2. **RPC Call Logging**
- The exporter now logs the number of RPC calls made per method every minute.
- Example log output:
  ```
  === SOLANA RPC CALLS IN LAST MINUTE ===
  getBlockProduction: 16
  getSlot: 24
  ...
  ```
- Use these logs to monitor and optimize your RPC usage.

### 3. **Per-Slot Granularity for Leader Slot Metrics**
- The exporter maintains per-slot granularity for leader slot metrics (processed/skipped),
  calling `getBlockProduction` for each leader slot individually.
- This ensures maximum accuracy for validator performance metrics.
- **Note:** This is more expensive in RPC/credits than batching, but provides the most detail.

### 4. **Optimizing RPC/Credit Usage**
- Increase `-slot-pace` to reduce frequency of expensive calls (e.g., 30s or 60s for lower cost).
- Only monitor the metrics you need.
- Use a dedicated, private RPC endpoint and rotate it if you suspect abuse.
