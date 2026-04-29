package ops

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/CatalinCapritaDIP/go-deploy.git/pkg/k8s"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
)

func Install(ctx context.Context, config *DeployConfiguration, releaseName string, releasePath string, values map[string]interface{}) error {
	slog.Info("Running install", "release", releaseName)

	settings := config.Settings
	installClient := action.NewInstall(config.Cfg)
	installClient.InsecureSkipTLSverify = true
	installClient.DependencyUpdate = true
	installClient.WaitStrategy = kube.StatusWatcherStrategy
	installClient.ReleaseName = releaseName
	installClient.DryRunStrategy = config.DryRunStrategy

	env := ctx.Value("env")
	if env == nil {
		return fmt.Errorf("no environemnt variables passed")
	}
	envMap, ok := env.(map[string]string)
	if !ok {
		return fmt.Errorf("env was not of type map[string]string")
	}
	charter, err := loadChart(installClient, releaseName, releasePath, settings, envMap)
	if err != nil {
		return err
	}
	type Msg struct {
		r *release.Release
		e error
	}
	msgChan := make(chan Msg, 1)
	go func() {
		defer close(msgChan)
		if config.StreamPodLogs {
			go k8s.StreamPodLogs(ctx, config.Settings, config.ClientSet, releaseName)
		}
		if config.DebugPort > 0 {
			go k8s.ForwardPort(ctx, config.Settings, config.ClientSet, releaseName, config.DebugPort)
		}
		resp, err := installClient.RunWithContext(ctx, charter, values)
		var msg Msg = Msg{}
		if err != nil {
			msg.e = fmt.Errorf("failed to run install operation: %w", err)
		}
		if releasev1 := ToReleaseV1(resp); releasev1 != nil {
			msg.r = releasev1
			if releasev1.Info.Status == common.StatusFailed {
				msg.e = fmt.Errorf("release failed: %s", releasev1.Info)
			}
		}
		msgChan <- msg

	}()
	select {
	case <-ctx.Done():
		if err = ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("Context cancelled before installation finished")
	case msg := <-msgChan:
		if msg.e != nil {
			return err
		}
		return nil
	}
}

func loadChart(installClient *action.Install, releaseName string, releasePath string, settings *cli.EnvSettings, env map[string]string) (chart.Charter, error) {
	var locationName string = ""
	if releasePath != "" {
		var err error
		if _, err = os.Stat(releasePath); err != nil {
			return nil, fmt.Errorf("failed to find release path on local machine: %w", err)
		}
		if locationName, err = filepath.Abs(releasePath); err != nil {
			return nil, fmt.Errorf("failed to find absolute path of release: %w", err)
		}
		installClient.OutputDir = "./out"
		slog.Debug("outputting into directory", "path", installClient.OutputDir)
	} else {
		// if name is not a filepath, set --repo flag
		if err := setCredentials(env, &installClient.ChartPathOptions); err != nil {
			return nil, fmt.Errorf("could not set remote repository credentials: %w", err)
		}
		locationName = releaseName
	}
	slog.Debug("Locating release", "name", locationName)
	chartPath, err := installClient.LocateChart(locationName, settings)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart %s :%w", releaseName, err)
	}
	charter, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart %s :%w", releaseName, err)
	}
	return charter, nil
}

func setCredentials(env map[string]string, opts *action.ChartPathOptions) error {
	username, ok := env[DOCKER_CREDS_USERNAME]
	if !ok {
		return fmt.Errorf("variable %s not set in env file", DOCKER_CREDS_USERNAME)
	}
	passw, ok := env[DOCKER_CREDS_PASSWORD]
	if !ok {
		return fmt.Errorf("variable %s not set in env file", DOCKER_CREDS_PASSWORD)
	}
	opts.Username = username
	opts.Password = passw
	opts.RepoURL = REPO_URL
	return nil
}
