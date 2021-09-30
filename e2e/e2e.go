package main

import (
	"context"
	"go/build"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/getoutreach/async/pkg/async"
	"github.com/getoutreach/gobox/pkg/sshhelper"
	localizerapi "github.com/getoutreach/localizer/api"
	"github.com/getoutreach/localizer/pkg/localizer"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
)

var virtualDeps = map[string][]string{
	// TODO(jaredallard): [DT-510] Store flagship dependencies in the outreach repository
	"flagship": {
		"outreach-templating-service",
		"olis",
		"mint",
		"giraffe",
		"outreach-accounts",
		"clientron",
	},
}

// Service is a mock of the service.yaml in bootstrap, which isn't currently
// open-sourced, yet!
type Service struct {
	Dependencies struct {
		// Optional is a list of OPTIONAL services e.g. the service can run / gracefully function without it running
		Optional []string `yaml:"optional"`

		// Required is a list of services that this service cannot function without
		Required []string `yaml:"required"`
	} `yaml:"dependencies"`
}

// BuildDependenciesList builds a list of dependencies by cloning them
// and appending them to the list. Deduplication is done and returned
// is a flat list of all of the dependencies of the initial root
// application who's dependency list was provided.
func BuildDependenciesList(ctx context.Context) ([]string, error) {
	deps := make(map[string]bool)

	a := sshhelper.GetSSHAgent()
	_, err := sshhelper.LoadDefaultKey("github.com", a, logrus.StandardLogger())
	if err != nil {
		return nil, err
	}

	auth := sshhelper.NewExistingSSHAgentCallback(a)

	f, err := os.Open("service.yaml")
	if err != nil {
		return nil, errors.Wrap(err, "failed to read service.yaml")
	}

	var s *Service
	err = yaml.NewDecoder(f).Decode(&s)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse service.yaml")
	}

	for _, d := range append(s.Dependencies.Required, s.Dependencies.Optional...) {
		err := grabDependencies(ctx, deps, d, auth)
		if err != nil {
			return nil, err
		}
	}

	depsList := make([]string, 0)
	for d := range deps {
		depsList = append(depsList, d)
	}

	return depsList, nil
}

// grabDependencies traverses the dependency tree by calculating
// it on the fly via git cloning of the dependencies. Passed in
// is a hash map used to prevent infinite recursion and de-duplicate
// dependencies. New dependencies are inserted into the provided hash-map
func grabDependencies(ctx context.Context, deps map[string]bool, name string, auth *sshhelper.ExistingSSHAgentCallback) error {
	fs := memfs.New()

	// Skip if we've already been downloaded
	if _, ok := deps[name]; ok {
		return nil
	}

	log.Info().Str("dep", name).Msg("Resolving dependency")

	var foundDeps []string

	// If we don't have a virtualDeps entry here, then download the git
	// repo, read service.yaml, and
	if _, ok := virtualDeps[name]; !ok {
		opts := &git.CloneOptions{
			URL:  "git@github.com:" + path.Join("getoutreach", name),
			Auth: auth,
		}
		_, err := git.CloneContext(ctx, memory.NewStorage(), fs, opts)
		if err != nil {
			return errors.Wrapf(err, "failed to clone dependency %s", name)
		}

		f, err := fs.Open("service.yaml")
		if err != nil {
			deps[name] = true
			log.Warn().Err(err).Msg("Failed to find service.yaml, will not try to calculate dependencies of this service")
			return nil
		}

		var s *Service
		err = yaml.NewDecoder(f).Decode(&s)
		if err != nil {
			return errors.Wrapf(err, "failed to parse service.yaml in dependency %s", name)
		}

		foundDeps = append(s.Dependencies.Required, s.Dependencies.Optional...)
	} else {
		log.Info().Msgf("Using baked-in dependency list")
		foundDeps = virtualDeps[name]
	}

	// Mark us as resolved to prevent inf dependency resolution
	// when we encounter cyclical dependency.
	deps[name] = true

	for _, d := range foundDeps {
		err := grabDependencies(ctx, deps, d, auth)
		if err != nil {
			return err
		}
	}

	return nil
}

func provisionNew(ctx context.Context, deps []string, target string) error {
	//nolint:errcheck // Why: Best effort remove existing cluster
	exec.CommandContext(ctx, "devenv", "--skip-update", "destroy").Run()

	cmd := exec.CommandContext(ctx, "devenv", "--skip-update", "provision", "--snapshot-target", target)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to provision devenv")
	}

	for _, d := range deps {
		// Skip dep with same name as our target
		if d == target {
			continue
		}

		log.Info().Msgf("Deploying dependency '%s'", d)
		cmd := exec.CommandContext(ctx, "devenv", "--skip-update", "deploy-app", d)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		err = cmd.Run()
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to deploy dependency '%s'", d)
		}
	}

	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// runEndToEndTests is a flag that denotes whether or not this needs to actually
	// run or not based off of the filepath.Walk function immediately proceeding.
	var runEndToEndTests bool

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if runEndToEndTests {
			// No need to keep walking through files if we've already found one file
			// that requries e2e tests.
			return nil
		}

		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			// Skip symlinks.
			return nil
		}

		if !strings.HasSuffix(path, "_test.go") {
			// Skip all files that aren't go test files.
			return nil
		}

		pkg, err := build.Default.Import(path, filepath.Base(path), build.ImportComment)
		if err != nil {
			return errors.Wrap(err, "import")
		}

		for _, tag := range pkg.AllTags {
			runEndToEndTests = runEndToEndTests || tag == "or_e2e"
		}

		return nil
	})

	if err != nil {
		// This err shouldn't fail the e2e tests, just warn on it.
		log.Warn().Err(err).Msg("Failed to walk repository to determine whether or not e2e tests needed to be ran")
	}

	// No or_e2e build tags were found.
	if !runEndToEndTests {
		log.Info().Msg("found no occurrences of or_e2e build tags, skipping e2e tests")
		os.Exit(0)
	}

	log.Info().Msg("Building dependency tree")

	deps, err := BuildDependenciesList(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to build dependency tree")
	}

	log.Info().Strs("deps", deps).Msg("Provisioning devenv")

	// TODO(jaredallard): outreach specific code
	target := "base"
	for _, d := range deps {
		if d == "flagship" {
			target = "flagship"
			break
		}
	}

	if err := exec.CommandContext(ctx, "devenv", "--skip-update", "status").Run(); err != nil {
		err = provisionNew(ctx, deps, target)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to create cluster")
		}
	} else {
		log.Info().
			Msg("Re-using existing cluster, this may lead to a non-reproducible failure/success. To ensure a clean operation, run `devenv destroy` before running tests")
	}

	log.Info().Msg("Deploying current application into cluster")
	cmd := exec.CommandContext(ctx, "devenv", "--skip-update", "deploy-app", ".")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to deploy current application into devenv")
	}

	log.Info().Msg("Running devconfig")
	cmd = exec.CommandContext(ctx, ".bootstrap/shell/devconfig.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to run devconfig")
	}

	if !localizer.IsRunning() {
		// Preemptively ask for sudo to prevent input mangaling with o.LocalApps
		log.Info().Msg("You may get a sudo prompt so localizer can create tunnels")
		cmd = exec.CommandContext(ctx, "sudo", "true")
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		err = cmd.Run()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to get root permissions")
		}

		log.Info().Msg("Starting devenv tunnel")
		cmd = exec.CommandContext(ctx, "devenv", "--skip-update", "tunnel")
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		err = cmd.Start()
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to start devenv tunnel")
		}

		for ctx.Err() == nil && !localizer.IsRunning() {
			async.Sleep(ctx, time.Second*1)
		}

		client, closer, err := localizer.Connect(ctx, grpc.WithBlock(), grpc.WithInsecure())
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to connect to localizer server to kill running instance")
		}
		defer closer()

		log.Info().Msg("Waiting for localizer (spawned by devenv tunnel) to be stable")
		for ctx.Err() == nil {
			resp, err := client.Stable(ctx, &localizerapi.Empty{})
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to determine if localizer was stable")
			}

			if resp.Stable {
				break
			}

			async.Sleep(ctx, time.Second*2)
		}

		// Defer the killing of the localizer server that was spawned.
		defer func() {
			log.Info().Msg("Killing the spawned localizer process (spawned by devenv tunnel)")

			if _, err := client.Kill(ctx, &localizerapi.Empty{}); err != nil {
				log.Warn().Err(err).Msg("failed to kill running localizer server")
			}
		}()
	}

	log.Info().Msg("Running e2e tests")
	cmd = exec.CommandContext(ctx, "./.bootstrap/shell/test.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err = cmd.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("E2E tests failed, or failed to run")
	}
}
