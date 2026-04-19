package scriptrunner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const scriptPath = ".deploy"

// Run executes the release stage script when present.
func Run(releaseDir, stage string, timeout time.Duration) (err error) {
	script := filepath.Join(releaseDir, scriptPath, stage)

	if _, err = os.Stat(script); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("stat script %q: %w", script, err)
	}

	if filepath.Base(script) != stage {
		return fmt.Errorf("invalid stage %q", stage)
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	//#nosec G204 -- script is constrained to <releaseDir>/.deploy/<stage> and stage must be a base name.
	cmd := exec.CommandContext(ctx, script)
	cmd.Dir = releaseDir
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err = cmd.Run(); err != nil {
		return fmt.Errorf("run script %q: %w", script, err)
	}

	return nil
}
