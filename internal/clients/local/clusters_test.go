package local

import (
	"fmt"
	"strings"
	"testing"

	localcore "github.com/dantofa/platform/internal/core/local"
)

// The kind config is hand-rendered YAML, where a single wrong indent silently
// produces a valid-but-wrong cluster (extraPortMappings landing outside the node
// entry, so the ingress is never published). Assert the exact document.
func TestKindConfigRendersIngressPublication(t *testing.T) {
	want := `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 80
        listenAddress: "127.0.0.1"
        protocol: TCP
      - containerPort: 30443
        hostPort: 443
        listenAddress: "127.0.0.1"
        protocol: TCP
  - role: worker
  - role: worker
`
	if got := kindConfig(1, 2); got != want {
		t.Errorf("kindConfig(1, 2) =\n%s\nwant:\n%s", got, want)
	}
}

// kind rejects a config that claims the same host port on two nodes, so the
// publication must be attached to exactly one node however many control-planes
// an HA cluster asks for.
func TestKindConfigPublishesOnOneNodeOnly(t *testing.T) {
	for _, controlPlanes := range []int{1, 2, 3} {
		got := kindConfig(controlPlanes, 1)
		if n := strings.Count(got, "extraPortMappings:"); n != 1 {
			t.Errorf("kindConfig(%d, 1) has %d extraPortMappings blocks, want exactly 1", controlPlanes, n)
		}
		if n := strings.Count(got, "role: control-plane"); n != controlPlanes {
			t.Errorf("kindConfig(%d, 1) has %d control-planes, want %d", controlPlanes, n, controlPlanes)
		}
	}
}

// The host↔node port pairs are a contract with the Traefik Service nodePorts in
// flux/local/traefik/release.yaml. Pin the render to the core spec so the two
// halves cannot drift through this layer unnoticed.
func TestKindConfigMatchesCoreIngressSpec(t *testing.T) {
	got := kindConfig(1, 0)
	mappings := localcore.IngressPortMappings()
	if len(mappings) == 0 {
		t.Fatal("core exposes no ingress port mappings")
	}
	for _, m := range mappings {
		entry := fmt.Sprintf("      - containerPort: %d\n        hostPort: %d\n", m.NodePort, m.HostPort)
		if !strings.Contains(got, entry) {
			t.Errorf("kindConfig missing mapping host %d -> node %d; got:\n%s", m.HostPort, m.NodePort, got)
		}
	}
}
