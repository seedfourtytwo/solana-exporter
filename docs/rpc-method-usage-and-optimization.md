# Solana Exporter: RPC Method Usage & Optimization

This document explains the purpose, usage, and optimization opportunities for each Solana RPC method used in your exporter.

---

## Table of Contents

- [getVersion](#getversion)
- [getVoteAccounts](#getvoteaccounts)
- [getSlot](#getslot)
- [getBalance](#getbalance)
- [getBlockProduction](#getblockproduction)
- [getEpochInfo](#getepochinfo)
- [getLeaderSchedule](#getleaderschedule)
- [minimumLedgerSlot](#minimumledgerslot)
- [getIdentity](#getidentity)
- [getFirstAvailableBlock](#getfirstavailableblock)
- [getHealth](#gethealth)
- [getInflationReward](#getinflationreward)
- [getBlock](#getblock)

---

## getVersion

- **Purpose:** Returns the Solana version running on the node.
- **Where Used:** `SolanaCollector.collectVersion()` (for Prometheus metric).
- **Current Frequency:** Called every Prometheus scrape (typically every 15s).
- **Optimization:**  
  - The version rarely changes.  
  - **Cache** the value and only refresh it every hour or on exporter restart.
  - Only update the metric if the version changes.

---

## getVoteAccounts

- **Purpose:** Returns info and stake for all voting accounts.
- **Where Used:**  
  - `collectVoteAccounts` (validator/cluster stake, last vote, root slot, delinquency, validator count)
  - `collectVoteAndRootDistance` (vote/root distance for validator)
  - `collectValidatorCommission` (validator commission)
  - Utility functions (e.g., mapping nodekeys to votekeys)
- **Current Frequency:**  
  - Called every Prometheus scrape (15s) for each metric above.
  - May be called multiple times per scrape if not deduplicated.
- **Optimization:**  
  - **Deduplicate** calls within a single scrape (fetch once, reuse for all metrics).
  - **Reduce frequency** if you don't need real-time updates (e.g., scrape every 60s).
  - In "light mode," skip cluster-wide metrics to reduce calls.

---

## getSlot

- **Purpose:** Returns the current slot at a given commitment level.
- **Where Used:**  
  - `collectVoteAndRootDistance` (to calculate distance from current slot)
- **Current Frequency:**  
  - Called every scrape, and possibly at a higher frequency if `FastMetricsInterval` is set.
- **Optimization:**  
  - **Cache** slot value for the duration of a scrape.
  - If using `FastMetricsInterval`, consider if you need sub-15s updates.

---

## getBalance

- **Purpose:** Returns the SOL balance for an account.
- **Where Used:**  
  - `collectBalances` (for all tracked addresses)
- **Current Frequency:**  
  - Called once per address per scrape.
- **Optimization:**  
  - **Batch** requests if possible (not natively supported by Solana, but you can parallelize).
  - **Reduce frequency** for less critical addresses.
  - Only track balances for essential accounts.

---

## getBlockProduction

- **Purpose:** Returns recent block production info.
- **Where Used:**  
  - For block production metrics (not shown in detail in the snippets, but likely similar to other metrics).
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Reduce frequency** (e.g., every 1-5 minutes).
  - Only fetch for relevant epochs/slots.

---

## getEpochInfo

- **Purpose:** Returns info about the current epoch.
- **Where Used:**  
  - For epoch-based calculations and metrics.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Cache** for the duration of an epoch (only refresh when slot/epoch changes).

---

## getLeaderSchedule

- **Purpose:** Returns the leader schedule for an epoch.
- **Where Used:**  
  - For leader schedule metrics and calculations.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Cache** for the duration of an epoch (only refresh when epoch changes).

---

## minimumLedgerSlot

- **Purpose:** Returns the lowest slot the node has in its ledger.
- **Where Used:**  
  - For node ledger state metrics.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Reduce frequency** (e.g., every 1-5 minutes).

---

## getIdentity

- **Purpose:** Returns the node's identity pubkey.
- **Where Used:**  
  - For node identity metrics.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Cache** value; only refresh on exporter restart or if node restarts.

---

## getFirstAvailableBlock

- **Purpose:** Returns the slot of the lowest confirmed block not purged from the ledger.
- **Where Used:**  
  - For ledger state metrics.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - **Reduce frequency** (e.g., every 1-5 minutes).

---

## getHealth

- **Purpose:** Returns the current health of the node.
- **Where Used:**  
  - For node health metrics.
- **Current Frequency:**  
  - Called every scrape.
- **Optimization:**  
  - This is a lightweight call, but you can reduce frequency if not needed in real-time.

---

## getInflationReward

- **Purpose:** Returns inflation/staking rewards for addresses for an epoch.
- **Where Used:**  
  - For inflation reward metrics (typically at epoch boundaries).
- **Current Frequency:**  
  - Called at epoch boundaries or on demand.
- **Optimization:**  
  - Only call at the end of each epoch or when new rewards are expected.

---

## getBlock

- **Purpose:** Returns identity and transaction info about a confirmed block.
- **Where Used:**  
  - For block-level metrics (e.g., transaction counts, rewards).
- **Current Frequency:**  
  - Called for specific slots/blocks as needed.
- **Optimization:**  
  - Only call for blocks of interest (e.g., recent or validator-produced blocks).

---

# General Optimization Strategies

- **Deduplicate**: Ensure each RPC call is made only once per scrape and reused for all relevant metrics.
- **Cache**: Cache values that rarely change (e.g., version, identity, leader schedule).
- **Reduce Scrape Frequency**: For non-critical metrics, scrape less often (e.g., every 1-5 minutes).
- **Light Mode**: Use light mode for node-specific metrics only.
- **Batch/Parallelize**: Where possible, batch or parallelize requests for efficiency.

---

# Example: Reducing getVersion Usage

Instead of calling `getVersion` every 15s, cache the value and only refresh it every hour or on exporter restart. This alone can reduce thousands of unnecessary calls per day.

--- 