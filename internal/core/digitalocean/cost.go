package digitalocean

import (
	"context"
	"fmt"
)

// DigitalOcean prices monthly resources by dividing the monthly rate by 730
// hours (its published convention), and bills block-storage volumes and the
// smallest Load Balancer at fixed rates that have no per-size API to read.
const (
	hoursPerMonth = 730.0
	// gibPerTB converts the Sizes API `transfer` field (documented in TB) to the
	// GiB unit DO bills bandwidth overage in ($0.01/GiB). Treated as TiB→GiB; the
	// per-cluster traffic figure is an estimate regardless (pooled allowance).
	gibPerTB = 1024.0
	// blockStorageGiBMonthlyUSD is DO's flat block-storage (Volumes) price, the
	// rate DOKS PersistentVolumes are billed at. Stable, no per-size API.
	blockStorageGiBMonthlyUSD = 0.10
	// loadBalancerMonthlyUSD is the price of the single-node regional Load
	// Balancer a DOKS `type: LoadBalancer` Service provisions by default.
	loadBalancerMonthlyUSD = 12.0
)

// Size is the subset of a DigitalOcean droplet size the cost model needs: its
// hourly price and included monthly transfer allowance. The clients adapter maps
// godo's Size onto this so the SDK type never reaches core.
type Size struct {
	Slug        string
	PriceHourly float64
	// TransferTB is the size's included monthly outbound transfer, in TB.
	TransferTB float64
}

// SizeNotFoundError is returned when a size slug is absent from the DO catalog.
type SizeNotFoundError struct{ Slug string }

func (e *SizeNotFoundError) Error() string {
	return fmt.Sprintf("no droplet size found matching %q", e.Slug)
}

// SizesAPI is the DO size-catalog surface the cost model depends on.
type SizesAPI interface {
	GetSize(ctx context.Context, slug string) (Size, error)
}

// CostSizingAPI is what ClusterCostModel needs: list clusters (to find the
// node-pool size by name) and look that size up in the catalog. The DO cluster
// client adapter satisfies both.
type CostSizingAPI interface {
	List(ctx context.Context) ([]Cluster, error)
	SizesAPI
}

// CostModel holds the per-cluster price constants a cost dashboard multiplies the
// always-collected cluster metrics (node count, PV bytes, LB count, egress) by.
// All are absolute rates, not caller choices — compute + transfer come from the
// DO Sizes API, storage + LB from DO's fixed published rates.
type CostModel struct {
	NodeHourlyUSD            float64 // one worker node's hourly price
	NodeTransferGiB          float64 // one node's included monthly transfer, GiB
	BlockStorageGiBHourlyUSD float64 // $/GiB-hour for a DOKS PersistentVolume
	LoadBalancerHourlyUSD    float64 // $/hour per DOKS LoadBalancer Service
}

// BuildCostModel derives the per-cluster cost constants from the worker size.
// The platform's single-node-pool invariant means one node price prices the whole
// pool exactly (node count comes from a live metric, so autoscaling is handled).
func BuildCostModel(size Size) CostModel {
	return CostModel{
		NodeHourlyUSD:            size.PriceHourly,
		NodeTransferGiB:          size.TransferTB * gibPerTB,
		BlockStorageGiBHourlyUSD: blockStorageGiBMonthlyUSD / hoursPerMonth,
		LoadBalancerHourlyUSD:    loadBalancerMonthlyUSD / hoursPerMonth,
	}
}

// ClusterCostModel resolves the named cluster's worker size and returns its cost
// model. It reads the first node pool's size — the opinionated builders provision
// exactly one, so that is the cluster's worker size.
func ClusterCostModel(ctx context.Context, client CostSizingAPI, name string) (CostModel, error) {
	clusters, err := client.List(ctx)
	if err != nil {
		return CostModel{}, err
	}
	cluster, ok := resolve(clusters, name)
	if !ok {
		return CostModel{}, &ClusterNotFoundError{Identifier: name}
	}
	if len(cluster.NodePools) == 0 {
		return CostModel{}, fmt.Errorf("cluster %q has no node pools to price", name)
	}
	size, err := client.GetSize(ctx, cluster.NodePools[0].Size)
	if err != nil {
		return CostModel{}, err
	}
	return BuildCostModel(size), nil
}
