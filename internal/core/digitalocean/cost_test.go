package digitalocean

import (
	"context"
	"errors"
	"math"
	"testing"
)

type fakeSizingAPI struct {
	clusters []Cluster
	sizes    map[string]Size
	sizeErr  error
}

func (f *fakeSizingAPI) List(context.Context) ([]Cluster, error) { return f.clusters, nil }
func (f *fakeSizingAPI) GetSize(_ context.Context, slug string) (Size, error) {
	if f.sizeErr != nil {
		return Size{}, f.sizeErr
	}
	s, ok := f.sizes[slug]
	if !ok {
		return Size{}, &SizeNotFoundError{Slug: slug}
	}
	return s, nil
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBuildCostModel(t *testing.T) {
	m := BuildCostModel(Size{Slug: "s-2vcpu-4gb", PriceHourly: 0.03571, TransferTB: 4})
	if !approx(m.NodeHourlyUSD, 0.03571) {
		t.Errorf("node hourly = %v, want 0.03571", m.NodeHourlyUSD)
	}
	if !approx(m.NodeTransferGiB, 4*1024) {
		t.Errorf("transfer GiB = %v, want 4096", m.NodeTransferGiB)
	}
	// Fixed DO rates divided by 730h.
	if !approx(m.BlockStorageGiBHourlyUSD, 0.10/730) {
		t.Errorf("block storage $/GiB-hr = %v", m.BlockStorageGiBHourlyUSD)
	}
	if !approx(m.LoadBalancerHourlyUSD, 12.0/730) {
		t.Errorf("LB $/hr = %v", m.LoadBalancerHourlyUSD)
	}
}

func TestClusterCostModel(t *testing.T) {
	f := &fakeSizingAPI{
		clusters: []Cluster{{
			Name:      "prod",
			NodePools: []NodePool{{Name: "system", Size: "s-4vcpu-8gb"}},
		}},
		sizes: map[string]Size{"s-4vcpu-8gb": {Slug: "s-4vcpu-8gb", PriceHourly: 0.07143, TransferTB: 5}},
	}
	m, err := ClusterCostModel(context.Background(), f, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approx(m.NodeHourlyUSD, 0.07143) || !approx(m.NodeTransferGiB, 5*1024) {
		t.Errorf("unexpected model: %+v", m)
	}
}

func TestClusterCostModelUnknownCluster(t *testing.T) {
	f := &fakeSizingAPI{}
	_, err := ClusterCostModel(context.Background(), f, "missing")
	var notFound *ClusterNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("want ClusterNotFoundError, got %v", err)
	}
}

func TestClusterCostModelNoNodePools(t *testing.T) {
	f := &fakeSizingAPI{clusters: []Cluster{{Name: "empty"}}}
	_, err := ClusterCostModel(context.Background(), f, "empty")
	if err == nil {
		t.Fatal("want error for cluster with no node pools")
	}
}
