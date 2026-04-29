package ops

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	"k8s.io/client-go/kubernetes"

	ri "helm.sh/helm/v4/pkg/release"
	release "helm.sh/helm/v4/pkg/release/v1"
)

const (
	REPO_URL              = "https://nexus.tools.dip-software.com/repository/leaseblocks"
	DOCKER_CREDS_USERNAME = "DOCKER_CREDS_USERNAME"
	DOCKER_CREDS_PASSWORD = "DOCKER_CREDS_PASSWORD"
)

type DeployConfiguration struct {
	// embed action configuration to access Configuration settings
	Cfg            *action.Configuration `json:"-"`
	Settings       *cli.EnvSettings      `json:"-"`
	ClientSet      *kubernetes.Clientset `json:"-"`
	StreamPodLogs  bool                  `json:"watch"`
	Verbose        bool                  `json:"verbose"`
	DryRunStrategy action.DryRunStrategy `json:"-"`
	DebugPort      int                   `json:"debugPort"`
}

func NewConfiguration(verbose, watch, dryRun bool, debugPort int) (*DeployConfiguration, error) {
	settings := cli.New()
	actionConfig := action.NewConfiguration()
	registryClient, err := newRegistryClient(settings)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate registry client: %w", err)
	}
	actionConfig.RegistryClient = registryClient
	actionConfig.Init(settings.RESTClientGetter(), settings.Namespace(), os.Getenv("HELM_DRIVER"))
	if actionConfig.Logger().Handler() != slog.Default().Handler() {
		slog.Warn("action config logger is not ok")
	}
	cs, err := kube.New(settings.RESTClientGetter()).Factory.KubernetesClientSet()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize k8s client set: %w", err)
	}
	var dryRunStrategy action.DryRunStrategy
	if dryRun {
		dryRunStrategy = action.DryRunClient
	} else {
		dryRunStrategy = action.DryRunNone
	}
	return &DeployConfiguration{
		Settings:       settings,
		ClientSet:      cs,
		Cfg:            actionConfig,
		Verbose:        verbose,
		StreamPodLogs:  watch,
		DryRunStrategy: dryRunStrategy,
		DebugPort:      debugPort,
	}, nil
}

func ToReleaseV1(releaser ri.Releaser) *release.Release {
	releasev1, ok := releaser.(release.Release)
	if !ok {
		return nil
	}
	return &releasev1
}
func newRegistryClient(settings *cli.EnvSettings) (*registry.Client, error) {
	tlsConfig := tls.Config{
		InsecureSkipVerify: true,
	}
	opts := []registry.ClientOption{
		registry.ClientOptDebug(true),
		registry.ClientOptWriter(os.Stderr),
		registry.ClientOptHTTPClient(&http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tlsConfig,
			},
		}),
	}
	client, err := registry.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate registry client: %w", err)
	}
	return client, nil
}
