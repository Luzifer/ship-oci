// Ship-OCI utility
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Luzifer/rconfig/v2"
	"github.com/sirupsen/logrus"

	"git.luzifer.io/luzifer/ship-oci/pkg/release"
)

var (
	cfg = struct {
		HookTimeout    time.Duration `flag:"hook-timeout" default:"1m" description:"How long to allow the hook-scripts to run"`
		ImageRef       string        `flag:"image-ref,i" default:"" description:"Image reference to pull (i.e. my.regist.ry/im/age:latest)"`
		KeepLast       int           `flag:"keep-last" default:"3" description:"How many releases to keep on disk, set to 1 to keep only the last"`
		LogLevel       string        `flag:"log-level" default:"info" description:"Log level (debug, info, warn, error, fatal)"`
		ReleaseDir     string        `flag:"release-dir,r" default:"releases" description:"Where to extract the images and hold the immutable data"`
		RunHooks       bool          `flag:"run-hooks" default:"false" description:"Execute the pre-activate and post-activate hooks from the new release"`
		VersionAndExit bool          `flag:"version" default:"false" description:"Prints current version and exits"`
	}{}

	version = "dev"
)

func initApp() error {
	rconfig.AutoEnv(true)
	if err := rconfig.ParseAndValidate(&cfg); err != nil {
		return fmt.Errorf("parsing cli options: %w", err)
	}

	if cfg.KeepLast < 1 {
		return fmt.Errorf("keep-last must be >= 1")
	}

	l, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("parsing log-level: %w", err)
	}
	logrus.SetLevel(l)

	return nil
}

func main() {
	var err error
	if err = initApp(); err != nil {
		logrus.WithError(err).Fatal("initializing app")
	}

	if cfg.VersionAndExit {
		logrus.WithField("version", version).Info("ship-oci")
		os.Exit(0)
	}

	opts := []release.Opt{
		release.WithKeepLast(cfg.KeepLast),
		release.WithReleaseDir(cfg.ReleaseDir),
	}

	if cfg.RunHooks {
		opts = append(opts, release.WithScriptRunsEnabled(cfg.HookTimeout))
	}

	rm := release.New(opts...)

	if err = rm.FetchAndLink(cfg.ImageRef, "current"); err != nil {
		logrus.WithError(err).Fatal("fetching lastest snapshot")
	}

	if err = rm.PruneOld(); err != nil {
		logrus.WithError(err).Fatal("removing old snapshots")
	}
}
