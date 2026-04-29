/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/CatalinCapritaDIP/go-deploy.git/gode/pkg/ops"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
)

type App string

const (
	ACCOUNTING           App    = "accounting"
	CLIENT360            App    = "client360"
	CMS                  App    = "cms"
	USER_MANAGER         App    = "user-manager"
	VALUES_TEMPLATE_FILE string = "values.template.yaml"
	VALUES_FILE          string = "values.go.yaml"
)

var validApps = [4]App{ACCOUNTING, CLIENT360, CMS, USER_MANAGER}
var envPath string
var apps []string
var verbose bool
var watch bool
var omitBackend bool
var omitFrontend bool
var all bool
var allApps = []string{string(ACCOUNTING), string(CLIENT360), string(CMS), string(USER_MANAGER)}
var dryRun bool
var rootDir string
var version string
var debugPort int
var valuesFileName string

// deployCmd represents the deploy command
var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploys a given microservice, both backend and frontend apps, or either part",
	Long: `Given a kubernetes cluster, this command uses helm in order to install a leaseblocks microservice
	deploying both the backend stack as well as the frontend one`,
	Run: deploy,
}

func init() {
	rootCmd.AddCommand(deployCmd)

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	localEnv := filepath.Join(cwd, "../", ".env.local")
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// deployCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	deployCmd.Flags().StringVarP(&envPath, "env", "e", localEnv, "sets env variables path")
	deployCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "sets verbosity level to DEBUG. otherwise, INFO")
	deployCmd.Flags().BoolVarP(&watch, "watch", "w", false, "whether or not to stream pod logs to STDERR")
	deployCmd.Flags().BoolVarP(&omitBackend, "backend", "B", false, "exclude backend API from deployment")
	deployCmd.Flags().BoolVarP(&omitFrontend, "frontend", "F", false, "exclude fronted from deployment")
	deployCmd.Flags().BoolVarP(&all, "all", "a", false, fmt.Sprintf("deploy all apps : %s", allApps))
	deployCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Whether to dry-run")
	deployCmd.Flags().StringVarP(&rootDir, "dir", "d", "", "Root directory where target .tgz files are located")
	deployCmd.Flags().StringVarP(&version, "version", "t", "", "Version of target .tgz files are located")
	deployCmd.Flags().StringVarP(&valuesFileName, "values", "f", "", "Values file to use for deployment")
	deployCmd.Flags().IntVarP(&debugPort, "debug", "p", -1, "Debug port to port-forward after installation, if desierd")
}

func deploy(c *cobra.Command, args []string) {
	configureLoggging()
	validateInput(args)
	env, err := parseEnv()
	if err != nil {
		slog.Error("could not parse env", "error", err.Error())
		os.Exit(1)
	}
	actionConfig, err := ops.NewConfiguration(verbose, watch, dryRun, debugPort)
	if err != nil {
		slog.Error("failed to initialize action configuration", "error", err.Error())
		os.Exit(1)
	}
	if cfg, err := json.Marshal(actionConfig); err == nil {
		slog.Debug("deploy configuration", "config", string(cfg))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	ctx = context.WithValue(ctx, "env", env)
	defer cancel()

	wg := sync.WaitGroup{}
	multiplier := 2
	if omitBackend || omitFrontend {
		multiplier = 1
	}
	if all {
		apps = allApps
	} else {
		apps = args
	}
	slog.Debug("started deployment", "apps", apps)
	wg.Add(len(apps) * multiplier)
	for _, app := range apps {
		if !omitBackend {
			go func() {
				defer wg.Done()
				var releaseName = fmt.Sprintf("%s-api", app)
				var releasePath = resolveReleasePath(releaseName)
				if err := doDeploy(ctx, releaseName, releasePath, env, actionConfig); err != nil {
					slog.Error("deployment failed", "release", releaseName, "error", err)
					return
				}
			}()
		}
		if !omitFrontend {
			go func() {
				defer wg.Done()
				fullName := fmt.Sprintf("%s-webapp", app)
				if err := doDeploy(ctx, fullName, "", env, actionConfig); err != nil {
					slog.Error("deployment failed", "release", fullName, "error", err)
					return
				}
			}()
		}

	}

	wg.Wait()

}

func configureLoggging() {
	var logLevel slog.Level
	if verbose {
		logLevel = slog.LevelDebug
	} else {
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	})))
}

func validateInput(args []string) {
	if all && len(args) != 0 {
		slog.Error("Both -all and -t targets specified. Select only one")
		os.Exit(0)
	}
	if !all && len(args) == 0 {
		slog.Error("no micrpservices selected. Exiting.")
		os.Exit(0)
	}

	if omitBackend && omitFrontend {
		slog.Error("both backend and frontend are omitted")
		os.Exit(1)
	}
	for _, app := range args {
		var found bool = false
		for _, va := range validApps {
			if app == string(va) {
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("app %s is invalid", app)
		}
	}
	if rootDir != "" && version == "" {
		slog.Error("root dir specified, but no chart version")
		os.Exit(0)
	}
}

func parseEnv() (map[string]string, error) {
	log.Printf("Using %s\n", envPath)
	bytes, err := os.ReadFile(envPath)
	if err != nil {
		return nil, err
	}
	vars, err := godotenv.UnmarshalBytes(bytes)
	if err != nil {
		return nil, err
	}
	return vars, nil
}

func resolveReleasePath(releaseName string) string {
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, fmt.Sprintf("%s-%s.tgz", releaseName, version))
}

func doDeploy(ctx context.Context, releaseName string, releasePath string, env map[string]string, cfg *ops.DeployConfiguration) error {
	// Parse variables from template into map
	deployVariables, err := replaceVariables(releaseName, env)
	if err != nil {
		return err
	}
	// Search release name
	releasePresent, err := checkIfReleasePresent(releaseName, cfg.Cfg)
	if err != nil {
		return err
	}
	if releasePresent && !dryRun {
		log.Printf("Release %s already exists. Uninstalling.\n", releaseName)
		if err = ops.UninstallContext(ctx, cfg.Cfg, releaseName); err != nil {
			return err
		}
		log.Printf("Previous revision of release %s uninstalled", releaseName)
	}
	if err != nil {
		return fmt.Errorf("failed to instantiate client set %w", err)
	}
	if err = ops.Install(ctx, cfg, releaseName, releasePath, deployVariables); err != nil {
		slog.Error("installation failed", "release", releaseName, "error", err)
		err = ops.Uninstall(cfg.Cfg, releaseName)
		if err != nil {
			return err
		}
		return nil
	}

	log.Printf("Release %s installed succesfully!\n", releaseName)
	return nil
}

func checkIfReleasePresent(target string, actionConfig *action.Configuration) (bool, error) {
	log.Printf("Looking for previous release of %s", target)
	listClient := action.NewList(actionConfig)
	listClient.Filter = target
	rel, err := listClient.Run()
	if err != nil {
		return false, err
	}
	return len(rel) > 0, nil
}

func replaceVariables(target string, variables map[string]string) (map[string]interface{}, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	var valuesTemplate []byte
	if valuesFileName == "" {
		slog.Info("reading from target dir")
		deploymentDir := filepath.Join(cwd, "../")
		targetDir := filepath.Join(deploymentDir, target)
		valuesTemplate, err = os.ReadFile(filepath.Join(targetDir, VALUES_TEMPLATE_FILE))

	} else {
		slog.Info("Reading provided values", "path", valuesFileName)
		valuesTemplate, err = os.ReadFile(valuesFileName)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read template file: %w", err)
	}
	replacements := []string{}
	for k, v := range variables {
		keyAsVar := fmt.Sprintf("${%s}", k)
		replacements = append(replacements, keyAsVar, v)
	}
	replacer := strings.NewReplacer(replacements...)
	parsedValues := replacer.Replace(string(valuesTemplate))
	parsedMap := map[string]any{}
	decoder := yaml.NewDecoder(strings.NewReader(parsedValues))
	if err := decoder.Decode(parsedMap); err != nil {
		return nil, fmt.Errorf("failed to decode parsed variables: \n %w", err)
	}
	return parsedMap, nil
}
