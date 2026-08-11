// Package flux holds the framework-free application logic for composing Flux
// GitOps on a cluster: installing Flux and registering/removing GitRepository
// sources and Kustomizations. The flux CLI surface is reached through the
// Engine interface, satisfied by the clients adapter, so this package imports
// neither cobra nor a client SDK and is reused by the future operator.
package flux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Defaults for the platform GitOps source a cluster is bootstrapped against.
// The composable commands and `do cluster bootstrap` share these so the base
// source/kustomization stay consistent.
const (
	DefaultSourceName   = "platform"
	DefaultSourceURL    = "https://github.com/dantofa/platform"
	DefaultSourceBranch = "master"
	// DefaultOCISourceURL is the published OCI artifact a remote cluster pulls
	// when bootstrapped with --source-type oci (the default). Local clusters
	// override this with the in-cluster kind registry URL.
	DefaultOCISourceURL = "oci://ghcr.io/dantofa/platform"
	// DefaultOCIRevision is the OCI tag a source tracks by default (mutable:
	// re-pulled every reconcile interval). DefaultSourceBranch is the git
	// equivalent.
	DefaultOCIRevision = "latest"
	// DefaultSourcePath is the shared, source-agnostic reconcile root every
	// cluster loads (Velero + Kyverno). Its nested Kustomizations reference the
	// source via ${source_kind}/${source_name}, filled in by the reconcile
	// root's postBuild.substituteFrom the cluster-vars ConfigMap.
	DefaultSourcePath = "./flux/cluster"
	// DefaultLocalSourcePath is the local/kind-only requirements root: the
	// SeaweedFS backend that stands in for a cloud bucket plus the backup
	// contract. It is only ever reconciled from OCI, so it hardcodes its source.
	DefaultLocalSourcePath = "./flux/local"

	// ClusterRootName is the reconcile root that loads the shared ./flux/cluster
	// stacks on every cluster type. LocalRequirementsRootName loads the
	// local-only ./flux/local requirements ahead of it on kind clusters.
	// IngressRootName loads a per-cluster-type ingress layer after the cluster
	// root (so ESO exists), e.g. ./flux/ingress/tunnel on kind.
	ClusterRootName           = "cluster"
	LocalRequirementsRootName = "local-requirements"
	IngressRootName           = "ingress"
	// DefaultTunnelIngressPath is the Cloudflare Tunnel ingress layer (outbound,
	// no LoadBalancer): the default on kind and, unless --dolb is given, on DOKS
	// too. DefaultRemoteIngressPath is the DOKS --dolb layer: Traefik + external-dns
	// behind a DO LoadBalancer, proxied by Cloudflare. Both set their controller as
	// the default IngressClass, so the same vanilla Ingress objects route on either.
	DefaultTunnelIngressPath = "./flux/ingress/tunnel"
	DefaultRemoteIngressPath = "./flux/ingress/traefik"
	// ExternalDNSRootName / DefaultExternalDNSPath is the DOKS DNS layer:
	// external-dns (Cloudflare). It is its own stack (controller-agnostic) and
	// DOKS-only — on kind the tunnel controller owns DNS.
	ExternalDNSRootName    = "external-dns"
	DefaultExternalDNSPath = "./flux/ingress/external-dns"
	// CloudflareAPITokenRootName / DefaultCloudflareAPITokenPath is the first half
	// of the ACME (Let's Encrypt) issuer layer: the Cloudflare DNS-01 token
	// ExternalSecret (cloudflare-api-token, in the cert-manager namespace) the
	// letsencrypt ClusterIssuer's solver reads. Split from the issuer itself so
	// its Secret is guaranteed synced before the issuer is applied. No ${...}
	// placeholders, so it carries no substitution.
	CloudflareAPITokenRootName    = "cloudflare-api-token"
	DefaultCloudflareAPITokenPath = "./flux/ingress/cloudflare-api-token"
	// LetsEncryptRootName / DefaultLetsEncryptPath is the ACME (Let's Encrypt)
	// ClusterIssuer, shared by both ACME --tls-issuer values (letsencrypt
	// production, staging CA) with the issuer name + ACME endpoint substituted per
	// cluster (${tls_issuer}, ${acme_server}). It dependsOn cloudflare-api-token
	// so the DNS-01 token Secret has synced before the issuer is applied:
	// cert-manager evaluates the solver on apply and pins the issuer InvalidSolver
	// — without re-reconciling — if the secret is missing, so the two must be
	// ordered, not co-applied. DOKS-only and deployed only for an ACME issuer;
	// selfsigned clusters use the always-present selfsigned issuer instead. The
	// Flux Kustomization is named "letsencrypt" on every cluster regardless of CA.
	LetsEncryptRootName    = "letsencrypt"
	DefaultLetsEncryptPath = "./flux/ingress/letsencrypt"
	// EchoRootName / DefaultEchoPath deploy the echo test backend. kind clusters
	// get it by default (after the ingress layer, so it is routable); it is
	// reusable on any cluster type via ./flux/echo.
	EchoRootName    = "echo"
	DefaultEchoPath = "./flux/echo"
	// MonitoringRootName / DefaultMonitoringPath is the opt-in in-cluster Grafana
	// server (a grafana-operator Grafana CR + Prometheus datasource), added only
	// when a cluster is bootstrapped with --monitoring. The collection layer (Alloy
	// + node-exporter + kube-state-metrics via k8s-monitoring) and the local
	// Prometheus store are unconditional base stacks (flux/cluster); this root only
	// adds the visualization server, so it is light — the heavy always-on pieces
	// live in the base.
	MonitoringRootName    = "monitoring"
	DefaultMonitoringPath = "./flux/monitoring"
	// MetricsRemoteRootName / DefaultMetricsRemotePath is the opt-in remote metrics
	// destination (--metrics-remote): a secondary in-cluster Prometheus receiver (a
	// Grafana-Cloud/remote stand-in for preview + local) plus a ConfigMap that adds
	// a second remote_write destination to the always-on k8s-monitoring Alloy, which
	// references it via an optional valuesFrom. Substitutes cluster-vars for the
	// receiver PVC (${storage_class}) and the destination's cluster external label
	// (${cluster_name}).
	MetricsRemoteRootName    = "metrics-remote"
	DefaultMetricsRemotePath = "./flux/test/prometheus"
	// ESOConfigName is the nested Kustomization holding the bitwarden
	// ClusterSecretStore; the ingress layer dependsOn it (cross-layer) so its
	// ExternalSecrets can sync.
	ESOConfigName = "eso-config"
	// GrafanaOperatorName / PrometheusName are base-stack Kustomizations (flux/cluster)
	// the monitoring root dependsOn: grafana-operator supplies the Grafana /
	// GrafanaDatasource CRDs the root's CRs need, and the prometheus stack owns the
	// `monitoring` namespace and the store the datasource points at.
	GrafanaOperatorName = "grafana-operator"
	PrometheusName      = "prometheus"
	// CertManagerConfigName is the nested Kustomization holding the selfsigned
	// ClusterIssuer; the Traefik ingress layer dependsOn it (cross-layer) so the
	// Certificate CRD is established and the selfsigned issuer exists.
	CertManagerConfigName = "cert-manager-config"

	// ClusterVarsName is the flux-system ConfigMap dctl writes at bootstrap with
	// this cluster's identity. Substituting reconcile roots pull it via
	// postBuild.substituteFrom, so the shared stacks (and downstream
	// Kustomizations) resolve ${source_kind}/${source_name}/${base_domain}/etc.
	// to per-cluster values. It is the single source of cluster-scoped variables.
	ClusterVarsName = "cluster-vars"
	// Cluster-vars keys. SourceKind/SourceName let the shared stacks bind their
	// sourceRef; BaseDomain/ClusterName are the cluster's ingress FQDN and name;
	// BitwardenOrgID/ProjectID scope the ESO ClusterSecretStore to a bws project.
	VarSourceKind         = "source_kind"
	VarSourceName         = "source_name"
	VarBaseDomain         = "base_domain"
	VarClusterName        = "cluster_name"
	VarBitwardenOrgID     = "bitwarden_org_id"
	VarBitwardenProjectID = "bitwarden_project_id"
	// VarTLSIssuer is the cert-manager ClusterIssuer name the DOKS Traefik default
	// certificate is issued by: TLSIssuerSelfSigned, TLSIssuerLetsEncrypt, or
	// TLSIssuerStaging. It also names the shared ACME ClusterIssuer and its
	// account-key secret (${tls_issuer}-account-key) in the letsencrypt layer.
	VarTLSIssuer = "tls_issuer"
	// VarACMEServer is the ACME directory endpoint the shared letsencrypt
	// ClusterIssuer registers against (${acme_server}) — the production or staging
	// Let's Encrypt CA, per ACMEServerURL(tls_issuer). Empty for selfsigned (the
	// ACME layer is not deployed), so nothing references it.
	VarACMEServer = "acme_server"
	// VarDNSZone is the cluster's Cloudflare zone apex (eTLD+1 of base_domain),
	// e.g. dantofa.dev. external-dns filters zones by their apex, so it must be
	// the registrable domain, not base_domain (a subdomain would exclude the zone).
	VarDNSZone = "dns_zone"
	// VarStorageClass is the Kubernetes StorageClass stateful stacks provision
	// their PVCs from (${storage_class}), written by dctl at bootstrap. It is per
	// cluster type — StorageClassDOKS or StorageClassLocal — so a portable manifest
	// binds to whichever the cluster provides (the class names differ, and a single
	// StorageClass manifest cannot span both provisioners).
	VarStorageClass = "storage_class"

	// clusterVarsNamespace is where the ConfigMap and reconcile roots live.
	clusterVarsNamespace = "flux-system"

	// ESONamespace is where the External Secrets Operator and its secret-zero
	// live; BitwardenTokenSecret/Key is the machine-account token the
	// ClusterSecretStore authenticates to Bitwarden Secrets Manager with.
	ESONamespace         = "external-secrets-system"
	BitwardenTokenSecret = "bitwarden-access-token"
	BitwardenTokenKey    = "token"
)

// SourceType selects which Flux source kind a cluster is bootstrapped against.
// oci is the default; git stays a first-class option for downstream projects
// that would rather track a branch than publish OCI artifacts.
type SourceType string

const (
	SourceOCI SourceType = "oci"
	SourceGit SourceType = "git"

	// DefaultSourceType is what a bootstrap registers unless --source-type says
	// otherwise.
	DefaultSourceType = SourceOCI
)

// FluxKind maps the CLI-facing source type to the Flux source CRD kind used in a
// Kustomization's sourceRef.
func (t SourceType) FluxKind() string {
	if t == SourceGit {
		return "GitRepository"
	}
	return "OCIRepository"
}

// DefaultRevision is the source revision tracked when none is given: the latest
// OCI tag, or the default git branch.
func (t SourceType) DefaultRevision() string {
	if t == SourceGit {
		return DefaultSourceBranch
	}
	return DefaultOCIRevision
}

// Engine is the flux-CLI surface this package depends on, satisfied by the
// clients adapter. It installs Flux and registers sources; the cluster-vars
// ConfigMap and reconcile roots go through Applier instead (the flux CLI can't
// set postBuild.substituteFrom). Create operations are create-or-update
// (idempotent).
type Engine interface {
	Install(ctx context.Context, version string) error
	CreateGitSource(ctx context.Context, name, url, branch string) error
	DeleteGitSource(ctx context.Context, name string) error
	CreateOCISource(ctx context.Context, name, url, tag string, insecure bool) error
	DeleteOCISource(ctx context.Context, name string) error
	DeleteKustomization(ctx context.Context, name string) error
}

// ReconcileRoot is a top-level Flux Kustomization dctl applies as a CR during
// bootstrap. When Substitute is set it carries a postBuild.substituteFrom the
// cluster-vars ConfigMap, so the portable stacks it reconciles resolve
// ${source_kind}/${source_name}/${base_domain}/etc. to this cluster's values.
// DependsOn orders it after other roots. Both are things `flux create
// kustomization` can't express, so bootstrap goes through the kube adapter.
type ReconcileRoot struct {
	Name       string
	Path       string
	SourceKind string // OCIRepository | GitRepository (this cluster's source)
	SourceName string
	DependsOn  []string // reconcile-root names in flux-system to wait for
	// Substitute pulls cluster-vars via postBuild.substituteFrom so a portable
	// (source-agnostic) tree binds to this cluster's values. Leave it off for
	// source-pinned trees with no ${...} placeholders, to avoid running
	// substitution over them.
	Substitute bool
}

// Applier is the cluster-side surface bootstrap needs beyond the flux CLI:
// writing the cluster-vars ConfigMap and applying reconcile roots as
// Kustomization CRs (both create-or-update). Satisfied by the kube adapter.
type Applier interface {
	ApplyConfigMap(ctx context.Context, namespace, name string, data map[string]string) error
	ApplyReconcileRoot(ctx context.Context, root ReconcileRoot) error
}

// SecretApplier plants a bootstrap secret (ensure a namespace, create-or-update a
// Secret). Satisfied by the kube adapter.
type SecretApplier interface {
	EnsureNamespace(ctx context.Context, name string) error
	ApplySecret(ctx context.Context, namespace, name string, data map[string][]byte, annotations map[string]string) error
}

// TLS issuer names — the cert-manager ClusterIssuer the Traefik default cert is
// issued by, selected per cluster at bootstrap (--tls-issuer). SelfSigned pairs
// with Cloudflare Full (no rate limits, no external dep); LetsEncrypt pairs with
// Full (strict) (production: a publicly-trusted cert via DNS-01); Staging is the
// same ACME/DNS-01 flow against Let's Encrypt's staging CA (preview/CI: high
// rate limits, untrusted certs — exercises the ACME path without spending the
// production CA's limits on ephemeral per-PR clusters).
const (
	TLSIssuerSelfSigned  = "selfsigned"
	TLSIssuerLetsEncrypt = "letsencrypt"
	TLSIssuerStaging     = "staging"
)

// ACME directory endpoints the shared letsencrypt ClusterIssuer registers
// against, selected by --tls-issuer (see ACMEServerURL). Production issues
// publicly-trusted certs under tight rate limits; staging is untrusted with high
// limits, for ephemeral preview/CI clusters.
const (
	ACMEServerLetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"
	ACMEServerStaging     = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// StorageClass names written as ${storage_class} per cluster type: DOKS ships the
// managed do-block-storage CSI class (default); kind ships the local-path
// "standard" class (default, already used by the local SeaweedFS store).
const (
	StorageClassDOKS  = "do-block-storage"
	StorageClassLocal = "standard"
)

// ACMEServerURL maps an ACME-backed --tls-issuer to its ACME directory endpoint,
// injected as the ${acme_server} cluster-var and substituted into the shared
// ClusterIssuer. Returns "" for a non-ACME issuer (selfsigned), which deploys no
// ACME layer, so the empty value is never referenced.
func ACMEServerURL(issuer string) string {
	switch issuer {
	case TLSIssuerLetsEncrypt:
		return ACMEServerLetsEncrypt
	case TLSIssuerStaging:
		return ACMEServerStaging
	default:
		return ""
	}
}

// DNSZone returns the registrable domain (eTLD+1) of an ingress base_domain,
// e.g. "preview.dantofa.dev" -> "dantofa.dev", "dantofa.com" -> "dantofa.com".
// This is the cluster's Cloudflare zone apex: external-dns filters zones by apex
// (a subdomain filter excludes the parent zone), so it is derived from the one
// base_domain input rather than configured separately.
func DNSZone(baseDomain string) (string, error) {
	zone, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(baseDomain, "."))
	if err != nil {
		return "", fmt.Errorf("deriving DNS zone from base domain %q: %w", baseDomain, err)
	}
	return zone, nil
}

// ValidateTLSIssuer rejects an unknown --tls-issuer value. The name is also the
// ClusterIssuer name substituted into the Traefik Certificate, so it must match
// an issuer the cluster deploys (selfsigned is always present; letsencrypt and
// staging are each added only for their value, via ACMEReconcileRoots).
func ValidateTLSIssuer(issuer string) error {
	switch issuer {
	case TLSIssuerSelfSigned, TLSIssuerLetsEncrypt, TLSIssuerStaging:
		return nil
	default:
		return fmt.Errorf("--tls-issuer must be %q, %q, or %q, got %q",
			TLSIssuerSelfSigned, TLSIssuerLetsEncrypt, TLSIssuerStaging, issuer)
	}
}

// ACMEReconcileRoots returns the reconcile roots that deploy the ACME
// ClusterIssuer for an ACME-backed --tls-issuer (letsencrypt or staging), and
// ok=false for selfsigned (no ACME layer; the selfsigned issuer ships in
// cert-manager-config). Both ACME issuers share the same two roots — same names
// and paths on every cluster — with the issuer identity carried by substitution
// (${tls_issuer}, ${acme_server}). The roots are returned in dependency order:
//
//  1. cloudflare-api-token — the Cloudflare DNS-01 token ExternalSecret. Needs
//     the bitwarden store (eso-config) to sync and the cert-manager namespace it
//     targets (cert-manager-config). No ${...} placeholders, so no substitution.
//  2. letsencrypt — the ClusterIssuer itself. Needs the ClusterIssuer CRD
//     (cert-manager-config) and, crucially, the token Secret already synced, so
//     it dependsOn cloudflare-api-token: cert-manager pins a freshly-applied
//     issuer InvalidSolver — without re-reconciling — if the secret is absent, so
//     the two must be ordered, not co-applied. Substituted for the issuer identity.
//
// The Traefik Certificate resolves against the issuer asynchronously once it is
// Ready. Callers must have passed ValidateTLSIssuer first, so an unknown value
// cannot reach here.
func ACMEReconcileRoots(issuer string) ([]ReconcileRoot, bool) {
	switch issuer {
	case TLSIssuerLetsEncrypt, TLSIssuerStaging:
		return []ReconcileRoot{
			{
				Name:      CloudflareAPITokenRootName,
				Path:      DefaultCloudflareAPITokenPath,
				DependsOn: []string{CertManagerConfigName, ESOConfigName},
			},
			{
				Name:       LetsEncryptRootName,
				Path:       DefaultLetsEncryptPath,
				DependsOn:  []string{CertManagerConfigName, CloudflareAPITokenRootName},
				Substitute: true,
			},
		}, true
	default:
		return nil, false
	}
}

// DOKSIngressRoots returns the ingress-layer reconcile roots for a DOKS cluster.
//
// The default (dolb=false) is the Cloudflare Tunnel controller — the same
// outbound-only, LoadBalancer-free ingress kind clusters use (DefaultTunnelIngressPath):
// Cloudflare terminates edge TLS and the controller owns DNS, so there is no DO
// LoadBalancer (the bulk of the cluster's cost), no external-dns, and no
// ACME/cert-manager cert. tlsIssuer is inert in this mode. The single root
// dependsOn eso-config (the controller pulls its cloudflare-api secret from
// bitwarden via ESO) and substitutes cluster-vars — tunnel_name is ${cluster_name},
// which the teardown tunnel-reap (ResolveTunnel/DeleteTunnelByName) resolves the
// tunnel by, so the DOKS tunnel lifecycle matches local's.
//
// With --dolb (dolb=true) it returns the LoadBalancer path unchanged: external-dns
// (Cloudflare DNS from Ingress status) + the ACME issuer layer when tlsIssuer is an
// ACME value (letsencrypt/staging) + Traefik (the default IngressClass, behind a DO
// LoadBalancer locked to Cloudflare's IPs). tlsIssuer names the ClusterIssuer the
// Traefik default cert is issued by; callers must ValidateTLSIssuer first.
func DOKSIngressRoots(dolb bool, tlsIssuer string) []ReconcileRoot {
	if !dolb {
		return []ReconcileRoot{
			{
				Name:       IngressRootName,
				Path:       DefaultTunnelIngressPath,
				DependsOn:  []string{ESOConfigName},
				Substitute: true,
			},
		}
	}
	// Traefik (ingress) and external-dns (DNS) are separate stacks. Traefik's
	// default cert is issued by cert-manager (${tls_issuer} ClusterIssuer), so it
	// waits on cert-manager-config (Certificate CRD + the always-present selfsigned
	// issuer); external-dns pulls its Cloudflare token from bws, so it waits on
	// eso-config.
	ingress := ReconcileRoot{
		Name:       IngressRootName,
		Path:       DefaultRemoteIngressPath,
		DependsOn:  []string{CertManagerConfigName},
		Substitute: true,
	}
	roots := []ReconcileRoot{
		{
			Name:       ExternalDNSRootName,
			Path:       DefaultExternalDNSPath,
			DependsOn:  []string{ESOConfigName},
			Substitute: true,
		},
	}
	// The ACME issuer layer (letsencrypt production or the staging CA) is added
	// only when the Traefik cert is issued by one; selfsigned needs none (it ships
	// in cert-manager-config). The ingress layer must also wait on the issuer: the
	// Traefik Certificate names the ${tls_issuer} ClusterIssuer, so that issuer
	// must exist before the Certificate is applied — else the CertificateRequest
	// fails IssuerNotFound and issuance stalls (the ACME issuer lives in its own
	// root, unlike the selfsigned one).
	if acme, ok := ACMEReconcileRoots(tlsIssuer); ok {
		ingress.DependsOn = append(ingress.DependsOn, LetsEncryptRootName)
		roots = append(roots, acme...)
	}
	return append(roots, ingress)
}

// MonitoringReconcileRoot returns the reconcile root for the opt-in in-cluster
// Grafana server (a Grafana CR + Prometheus datasource + ingress), appended when
// --monitoring is set. It dependsOn eso-config (the generated Grafana admin
// ExternalSecret needs ESO), grafana-operator (the Grafana/GrafanaDatasource CRDs
// its CRs are), and prometheus (which owns the `monitoring` namespace and is the
// datasource's store). It substitutes cluster-vars so the nested sourceRefs
// (${source_kind}/${source_name}) and the Grafana ingress host (${base_domain})
// resolve per cluster.
func MonitoringReconcileRoot() ReconcileRoot {
	return ReconcileRoot{
		Name:       MonitoringRootName,
		Path:       DefaultMonitoringPath,
		DependsOn:  []string{ESOConfigName, GrafanaOperatorName, PrometheusName},
		Substitute: true,
	}
}

// MetricsRemoteReconcileRoot returns the reconcile root for the opt-in remote
// metrics destination (--metrics-remote). It deploys a secondary Prometheus
// receiver and the k8s-monitoring-remote-destination ConfigMap that the always-on
// k8s-monitoring HelmRelease merges via valuesFrom, adding a second remote_write
// destination (the base stack keeps only the local destination when this root is
// absent). dependsOn prometheus for the `monitoring` namespace and the
// prometheus-community HelmRepository it reuses; substitutes cluster-vars for the
// receiver PVC (${storage_class}) and the cluster external label (${cluster_name}).
func MetricsRemoteReconcileRoot() ReconcileRoot {
	return ReconcileRoot{
		Name:       MetricsRemoteRootName,
		Path:       DefaultMetricsRemotePath,
		DependsOn:  []string{PrometheusName},
		Substitute: true,
	}
}

// ValidateBitwardenConfig guards against a half-configured Bitwarden setup. When
// a ClusterSecretStore is being scoped (a project or organization ID is given)
// but the machine-account token is missing, ESO can never authenticate, secret-
// zero is skipped, and every stack behind eso-config hangs until Flux times out
// minutes later. Fail fast at bootstrap with an actionable message instead. An
// entirely empty trio is allowed (bitwarden simply not configured for the
// cluster), matching ProvisionESOAccessToken's no-op-on-empty contract.
func ValidateBitwardenConfig(token, projectID, orgID string) error {
	if token != "" || (projectID == "" && orgID == "") {
		return nil
	}
	return errors.New("bitwarden project/organization configured but no access token: " +
		"set --bitwarden-token or $BWS_ACCESS_TOKEN (note: `bws run` strips " +
		"BWS_ACCESS_TOKEN from the child environment, so pass it explicitly)")
}

// ProvisionESOAccessToken plants secret-zero: the Bitwarden machine-account token
// the ESO ClusterSecretStore authenticates with. Idempotent; a no-op when token
// is empty (bitwarden not configured for this cluster, so the store stays
// unauthenticated and its ExternalSecrets will not sync).
func ProvisionESOAccessToken(ctx context.Context, a SecretApplier, token string) error {
	if token == "" {
		return nil
	}
	if err := a.EnsureNamespace(ctx, ESONamespace); err != nil {
		return err
	}
	return a.ApplySecret(ctx, ESONamespace, BitwardenTokenSecret,
		map[string][]byte{BitwardenTokenKey: []byte(token)}, nil)
}

// SourceSpec describes a Flux source to register: its Type (oci/git) selects the
// source CRD kind and how Revision is read (an OCI tag or a git branch).
// Insecure allows plain-HTTP OCI, for the in-cluster kind registry only.
type SourceSpec struct {
	Type     SourceType
	Name     string
	URL      string
	Revision string
	Insecure bool
}

// KustomizationSpec describes a Kustomization reconciling a path from a source.
// Type selects the source CRD kind (oci/git) the sourceRef points at. Substitute
// and DependsOn are the reconcile-root capabilities exposed to callers (a
// downstream project layering its own payload): Substitute binds ${...} tokens
// in the manifests to cluster-vars (e.g. ${base_domain}); DependsOn orders the
// payload after platform layers (e.g. "ingress") so it is not applied early.
type KustomizationSpec struct {
	Type       SourceType
	Name       string
	Source     string
	Path       string
	DependsOn  []string
	Substitute bool
}

// SourceResult reports a registered source.
type SourceResult struct {
	Source   string `json:"source"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
}

// KustomizationResult reports a registered kustomization.
type KustomizationResult struct {
	Kustomization string `json:"kustomization"`
	SourceKind    string `json:"source_kind"`
	Source        string `json:"source"`
	Path          string `json:"path"`
}

// BootstrapResult reports the source and reconcile roots a bootstrap registered.
type BootstrapResult struct {
	Source         string   `json:"source"`
	SourceKind     string   `json:"source_kind"`
	URL            string   `json:"url"`
	Revision       string   `json:"revision"`
	Kustomizations []string `json:"kustomizations"`
}

// AddSource registers (create-or-update) an OCIRepository or GitRepository
// source per spec.Type.
func AddSource(ctx context.Context, e Engine, spec SourceSpec) (SourceResult, error) {
	switch spec.Type {
	case SourceGit:
		if err := e.CreateGitSource(ctx, spec.Name, spec.URL, spec.Revision); err != nil {
			return SourceResult{}, err
		}
	default:
		if err := e.CreateOCISource(ctx, spec.Name, spec.URL, spec.Revision, spec.Insecure); err != nil {
			return SourceResult{}, err
		}
	}
	return SourceResult{
		Source: spec.Name, Kind: spec.Type.FluxKind(), URL: spec.URL, Revision: spec.Revision,
	}, nil
}

// RemoveSource deletes a source of the given type.
func RemoveSource(ctx context.Context, e Engine, typ SourceType, name string) error {
	if typ == SourceGit {
		return e.DeleteGitSource(ctx, name)
	}
	return e.DeleteOCISource(ctx, name)
}

// AddKustomization registers (create-or-update) a Kustomization referencing a
// source of spec.Type. It applies a reconcile-root CR through the kube Applier
// (not `flux create kustomization`) so it can carry postBuild.substituteFrom and
// dependsOn — the capabilities a downstream payload needs, and the same CR shape
// bootstrap's own roots use.
func AddKustomization(ctx context.Context, a Applier, spec KustomizationSpec) (KustomizationResult, error) {
	kind := spec.Type.FluxKind()
	root := ReconcileRoot{
		Name:       spec.Name,
		Path:       spec.Path,
		SourceKind: kind,
		SourceName: spec.Source,
		DependsOn:  spec.DependsOn,
		Substitute: spec.Substitute,
	}
	if err := a.ApplyReconcileRoot(ctx, root); err != nil {
		return KustomizationResult{}, err
	}
	return KustomizationResult{
		Kustomization: spec.Name, SourceKind: kind, Source: spec.Source, Path: spec.Path,
	}, nil
}

// RemoveKustomization deletes a Kustomization.
func RemoveKustomization(ctx context.Context, e Engine, name string) error {
	return e.DeleteKustomization(ctx, name)
}

// KustomizationStatus is one Flux Kustomization's reconciliation state. Status is
// the kstatus verdict (Current/InProgress/Failed/...); Ready is the gate.
type KustomizationStatus struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Ready     bool   `json:"ready"`
	Message   string `json:"message,omitempty"`
}

// KustomizationStatuser reads the reconciliation status of the Flux
// Kustomizations on a cluster (satisfied by the kube adapter, via kstatus).
type KustomizationStatuser interface {
	KustomizationStatuses(ctx context.Context, namespace string) ([]KustomizationStatus, error)
}

// ListKustomizations returns every Kustomization's status (never nil, so an empty
// cluster renders as a JSON `[]`).
func ListKustomizations(ctx context.Context, s KustomizationStatuser, namespace string) ([]KustomizationStatus, error) {
	statuses, err := s.KustomizationStatuses(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if statuses == nil {
		statuses = []KustomizationStatus{}
	}
	return statuses, nil
}

// VerifyKustomizations returns every Kustomization's status plus whether all are
// ready — the gate: ok is false if any Kustomization is not reconciled.
func VerifyKustomizations(ctx context.Context, s KustomizationStatuser, namespace string) (statuses []KustomizationStatus, ok bool, err error) {
	statuses, err = ListKustomizations(ctx, s, namespace)
	if err != nil {
		return nil, false, err
	}
	ok = true
	for _, st := range statuses {
		if !st.Ready {
			ok = false
		}
	}
	return statuses, ok, nil
}

// VerifyKustomizationsWait polls VerifyKustomizations until every Kustomization
// is ready or the timeout elapses, returning the last statuses + ok either way
// (so a timed-out gate still reports what is not reconciled). It turns the
// snapshot gate into a convergence gate for CI after a bootstrap/apply.
func VerifyKustomizationsWait(ctx context.Context, s KustomizationStatuser, namespace string, timeout, interval time.Duration) (statuses []KustomizationStatus, ok bool, err error) {
	deadline := time.Now().Add(timeout)
	for {
		statuses, ok, err = VerifyKustomizations(ctx, s, namespace)
		if err != nil {
			return nil, false, err
		}
		if ok || !time.Now().Before(deadline) {
			return statuses, ok, nil
		}
		select {
		case <-ctx.Done():
			return statuses, ok, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Bootstrap installs Flux, registers the source (oci or git per src.Type), writes
// the cluster-vars ConfigMap (this cluster's source coordinates merged with the
// caller's vars, e.g. base_domain/cluster_name), then applies the given reconcile
// roots as Kustomization CRs in order. Each root's SourceKind/SourceName are
// filled from the registered source, so callers pass roots describing only the
// paths and ordering. This one sequence serves every cluster: DOKS passes a
// single `cluster` root, kind passes `local-requirements` then `cluster`.
func Bootstrap(ctx context.Context, e Engine, a Applier, version string, src SourceSpec, vars map[string]string, roots []ReconcileRoot) (BootstrapResult, error) {
	if err := e.Install(ctx, version); err != nil {
		return BootstrapResult{}, err
	}
	if _, err := AddSource(ctx, e, src); err != nil {
		return BootstrapResult{}, err
	}
	kind := src.Type.FluxKind()

	// The cluster-vars ConfigMap the substituting roots read: the source
	// coordinates (always) plus the caller's cluster identity, which the roots
	// must be able to resolve before they reconcile.
	clusterVars := map[string]string{VarSourceKind: kind, VarSourceName: src.Name}
	for k, v := range vars {
		clusterVars[k] = v
	}
	if err := a.ApplyConfigMap(ctx, clusterVarsNamespace, ClusterVarsName, clusterVars); err != nil {
		return BootstrapResult{}, err
	}

	names := make([]string, 0, len(roots))
	for _, r := range roots {
		r.SourceKind, r.SourceName = kind, src.Name
		if err := a.ApplyReconcileRoot(ctx, r); err != nil {
			return BootstrapResult{}, err
		}
		names = append(names, r.Name)
	}
	return BootstrapResult{
		Source: src.Name, SourceKind: kind, URL: src.URL,
		Revision: src.Revision, Kustomizations: names,
	}, nil
}
