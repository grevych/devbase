// Copyright 2022 Outreach Corporation. All Rights Reserved.

// Description: This file has the package main.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/getoutreach/gobox/pkg/box"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

// flagship is the name of the flagship
const flagship = "flagship"

// DevenvConfig is a struct that contains the devenv configuration
// which is usually called "devenv.yaml". This also works for the
// legacy service.yaml format.
type DevenvConfig struct {
	// Service denotes if this repository is a service.
	Service bool `yaml:"service"`

	Dependencies struct {
		// Optional is a list of OPTIONAL services e.g. the service can run / gracefully function without it running
		Optional []string `yaml:"optional"`

		// Required is a list of services that this service cannot function without
		Required []string `yaml:"required"`
	} `yaml:"dependencies"`
}

// osStdinOut is a helper function to use the os stdin/out/err
func osStdInOutErr(c *exec.Cmd) *exec.Cmd {
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c
}

// BuildDependenciesList builds a list of dependencies by cloning them
// and appending them to the list. Deduplication is done and returned
// is a flat list of all of the dependencies of the initial root
// application who's dependency list was provided.
func BuildDependenciesList(ctx context.Context) ([]string, error) {
	deps := make(map[string]struct{})

	s, err := parseDevenvConfig("devenv.yaml")
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse devenv.yaml")
	}

	for _, d := range append(s.Dependencies.Required, s.Dependencies.Optional...) {
		if err := grabDependencies(ctx, deps, d); err != nil {
			return nil, err
		}
	}

	depsList := make([]string, 0)
	for d := range deps {
		depsList = append(depsList, d)
	}

	return depsList, nil
}

// findDependenciesInRepo finds the dependencies in a repository
// at all of the possible paths
func findDependenciesInRepo(serviceName, repoPath string) map[string]struct{} {
	possibleFiles := []string{"devenv.yaml", "noncompat-service.yaml", "service.yaml"}

	var dc *DevenvConfig
	for _, f := range possibleFiles {
		if _, err := os.Stat(filepath.Join(repoPath, f)); err != nil {
			continue
		}

		var err error
		dc, err = parseDevenvConfig(filepath.Join(repoPath, f))
		if err != nil {
			continue
		}

		// We found a file, stop looking
		break
	}

	if dc == nil {
		log.Warn().Str("service", serviceName).
			Msgf("Failed to find any of the following %v, will not try to calculate dependencies of this service", possibleFiles)
		return nil
	}

	deps := make(map[string]struct{})
	for _, d := range append(dc.Dependencies.Required, dc.Dependencies.Optional...) {
		deps[d] = struct{}{}
	}

	return deps
}

// grabDependencies traverses the dependency tree by calculating
// it on the fly via git cloning of the dependencies. Passed in
// is a hash map used to prevent infinite recursion and de-duplicate
// dependencies. New dependencies are inserted into the provided hash-map
func grabDependencies(ctx context.Context, deps map[string]struct{}, serviceName string) error {
	// We special case this here to ensure we don't fail on deps that haven't updated
	// their dependency yet.
	if serviceName == flagship {
		serviceName = "outreach"
	}

	// Skip if we've already been downloaded
	if _, ok := deps[serviceName]; ok {
		return nil
	}

	log.Info().Str("dep", serviceName).Msg("Resolving dependency")

	// Create a temporary directory to clone the repo into
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("e2e-%s-*", serviceName))
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(tmpDir)

	// Download the git repo locally
	//nolint:gosec // Why: Need to do this
	cmd := exec.CommandContext(ctx,
		"git", "clone", "-q", "--depth", "1", "git@github.com:"+path.Join("getoutreach", serviceName), tmpDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return errors.Wrapf(err, "failed to clone dependency %s", serviceName)
	}

	// Find the dependencies of this repo
	foundDeps := findDependenciesInRepo(serviceName, tmpDir)

	// Mark us as resolved to prevent inf dependency resolution
	// when we encounter cyclical dependency.
	deps[serviceName] = struct{}{}

	for d := range foundDeps {
		if err := grabDependencies(ctx, deps, d); err != nil {
			return err
		}
	}

	return nil
}

// parseDevenvConfig parses the devenv.yaml file and returns a struct
func parseDevenvConfig(path string) (*DevenvConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read devenv.yaml or service.yaml")
	}
	defer f.Close()

	var dc DevenvConfig
	if err := yaml.NewDecoder(f).Decode(&dc); err != nil {
		return nil, errors.Wrapf(err, "failed to parse devenv.yaml or service.yaml")
	}

	return &dc, nil
}

// appAlreadyDeployed checks if an application is already deployed, if it is
// it returns true, otherwise false.
func appAlreadyDeployed(ctx context.Context, app string) bool {
	var deployedApps []struct {
		Name string `json:"name"`
	}

	b, err := exec.CommandContext(ctx, "devenv", "--skip-update", "apps", "list", "--output", "json").Output()
	if err != nil {
		return false
	}

	if err := json.Unmarshal(b, &deployedApps); err != nil {
		return false
	}

	for _, a := range deployedApps {
		if a.Name == app {
			return true
		}
	}

	return false
}

// provisionNew destroys and re-provisions a devenv
func provisionNew(ctx context.Context, deps []string, target string) error { // nolint:unparam // Why: keeping in the interface for now
	//nolint:errcheck // Why: Best effort remove existing cluster
	exec.CommandContext(ctx, "devenv", "--skip-update", "destroy").Run()

	if err := osStdInOutErr(exec.CommandContext(ctx, "devenv", "--skip-update",
		"provision", "--snapshot-target", target)).Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to provision devenv")
	}

	for _, d := range deps {
		// Skip applications that are already deployed, this is usually when
		// they're in a snapshot we just provisioned from.
		if appAlreadyDeployed(ctx, d) {
			log.Info().Msgf("App %s already deployed, skipping", d)
			continue
		}

		log.Info().Msgf("Deploying dependency '%s'", d)
		if err := osStdInOutErr(exec.CommandContext(ctx, "devenv", "--skip-update", "apps", "deploy", d)).Run(); err != nil {
			log.Fatal().Err(err).Msgf("Failed to deploy dependency '%s'", d)
		}
	}

	return nil
}

// shouldRunE2ETests denotes whether or not this needs to actually
// run
func shouldRunE2ETests() (bool, error) {
	var runEndToEndTests bool

	build.Default.BuildTags = []string{"or_test", "or_e2e"}

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if runEndToEndTests {
			// No need to keep walking through files if we've already found one file
			// that requires e2e tests.
			return nil
		}

		if err != nil {
			return err
		}

		if info.IsDir() && path != "." {
			// Skip submodule directories.
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			}
		}

		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			// Skip symlinks.
			return nil
		}

		if !strings.HasSuffix(path, "_test.go") {
			// Skip all files that aren't go test files.
			return nil
		}

		pkg, err := build.Import(path, filepath.Base(path), build.ImportComment)
		if err != nil {
			// Skip files that are not compatible with current build tags like or_int
			var noGoErr *build.NoGoError
			if errors.As(err, &noGoErr) {
				return nil
			}
			return errors.Wrap(err, "import")
		}

		for _, tag := range pkg.AllTags {
			runEndToEndTests = runEndToEndTests || tag == "or_e2e"
		}

		return nil
	})
	return runEndToEndTests, err
}

func main() { //nolint:funlen,gocyclo // Why: there are no reusable parts to extract
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	conf, err := box.EnsureBoxWithOptions(ctx)
	if err != nil {
		//nolint:gocritic // Why: We're OK with this.
		log.Fatal().Err(err).Msg("Failed to load box config")
	}

	if conf.DeveloperEnvironmentConfig.VaultConfig.Enabled {
		vaultAddr := conf.DeveloperEnvironmentConfig.VaultConfig.Address
		if os.Getenv("CI") == "true" { //nolint:goconst // Why: true == true
			vaultAddr = conf.DeveloperEnvironmentConfig.VaultConfig.AddressCI
		}
		log.Info().Str("vault-addr", vaultAddr).Msg("Set Vault Address")
		os.Setenv("VAULT_ADDR", vaultAddr)
	}

	// No or_e2e build tags were found.
	runE2ETests, err := shouldRunE2ETests()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to determine if e2e tests should be run")
	}
	if !runE2ETests {
		log.Info().Msg("found no occurrences of or_e2e build tags, skipping e2e tests")
		return
	}

	log.Info().Msg("Building dependency tree")

	deps, err := BuildDependenciesList(ctx)
	if err != nil {
		//nolint:gocritic // Why: need to get exit code >0
		log.Fatal().Err(err).Msg("Failed to build dependency tree")
		return
	}

	log.Info().Strs("deps", deps).Msg("Provisioning devenv")

	// TODO(jaredallard): outreach specific code
	target := "base"
	for _, d := range deps {
		if d == "outreach" {
			target = flagship
			break
		}
	}

	// Provision a devenv if it doesn't already exist. If it does exist,
	// warn the user their test is no longer potentially reproducible.
	// Allow skipping provision, this is generally only useful for the devenv
	// which uses this framework -- but provisions itself.
	if os.Getenv("SKIP_DEVENV_PROVISION") != "true" {
		if exec.CommandContext(ctx, "devenv", "--skip-update", "status").Run() != nil {
			if err := provisionNew(ctx, deps, target); err != nil {
				//nolint:gocritic // Why: need to get exit code >0
				log.Fatal().Err(err).Msg("Failed to create cluster")
			}
		} else {
			log.Info().
				//nolint:lll // Why: Message to user
				Msg("Re-using existing cluster, this may lead to a non-reproducible failure/success. To ensure a clean operation, run `devenv destroy` before running tests")
		}
	}

	dc, err := parseDevenvConfig("devenv.yaml")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse devenv.yaml, cannot run e2e tests for this repo")
	}

	// if it's a library we don't need to deploy the application.
	if dc.Service {
		log.Info().Msg("Deploying current application into cluster")
		if osStdInOutErr(exec.CommandContext(ctx, "devenv", "--skip-update", "apps", "deploy", ".")).Run() != nil {
			log.Fatal().Err(err).Msg("Failed to deploy current application into devenv")
		}
	}

	log.Info().Msg("Running devconfig")
	if err := osStdInOutErr(exec.CommandContext(ctx, ".bootstrap/shell/devconfig.sh")).Run(); err != nil {
		log.Fatal().Err(err).Msg("Failed to run devconfig")
	}

	// Allow users to opt out of running localizer
	if os.Getenv("SKIP_LOCALIZER") != "true" {
		closer, err := runLocalizer(ctx)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to run localizer")
		}
		defer closer()
	}

	log.Info().Msg("Running e2e tests")
	os.Setenv("TEST_TAGS", "or_test,or_e2e")
	if err := osStdInOutErr(exec.CommandContext(ctx, "./.bootstrap/shell/test.sh")).Run(); err != nil {
		log.Fatal().Err(err).Msg("E2E tests failed, or failed to run")
	}
}
