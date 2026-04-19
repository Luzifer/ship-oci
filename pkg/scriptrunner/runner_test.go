package scriptrunner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunExecutesScript(t *testing.T) {
	releaseDir := t.TempDir()
	createScript(t, releaseDir, "pre-activate", "#!/bin/sh\necho ok > hook-output\n")

	require.NoError(t, Run(releaseDir, "pre-activate", time.Second))

	assert.Equal(t, "ok\n", readTestFile(t, filepath.Join(releaseDir, "hook-output")))
}

func TestRunReturnsErrorOnNonZeroExit(t *testing.T) {
	releaseDir := t.TempDir()
	createScript(t, releaseDir, "post-activate", "#!/bin/sh\nexit 3\n")

	err := Run(releaseDir, "post-activate", time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run script")
}

func TestRunReturnsErrorOnTimeout(t *testing.T) {
	releaseDir := t.TempDir()
	createScript(t, releaseDir, "pre-activate", "#!/bin/sh\nsleep 1\n")

	err := Run(releaseDir, "pre-activate", 10*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run script")
}

func TestRunReturnsNilWhenScriptDoesNotExist(t *testing.T) {
	require.NoError(t, Run(t.TempDir(), "pre-activate", time.Second))
}

func createScript(t *testing.T, releaseDir, stage, body string) {
	t.Helper()

	scriptDir := filepath.Join(releaseDir, scriptPath)
	require.NoError(t, os.MkdirAll(scriptDir, 0o750))

	scriptPath := filepath.Join(scriptDir, stage)
	require.NoError(t, os.WriteFile(scriptPath, []byte(body), 0o600))
	//#nosec G302 -- Test fixture scripts must be executable to exercise script execution.
	require.NoError(t, os.Chmod(scriptPath, 0o700))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	//#nosec G304 -- Test helper reads files created in t.TempDir with fixed filenames.
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}
