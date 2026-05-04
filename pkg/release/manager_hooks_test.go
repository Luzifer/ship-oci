package release

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1" //revive:disable-line:redundant-import-alias // enforced by goimports
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchAndLinkPostActivateFailureReturnsErrorAfterLinking(t *testing.T) {
	restore := stubReleaseDeps()
	defer restore()

	releaseDir := t.TempDir()
	mgr := New(
		WithReleaseDir(releaseDir),
		WithKeepLast(1),
		WithScriptRunsEnabled(time.Second),
	)

	const digest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	releaseID := strings.Replace(digest, ":", "-", 1)
	releasePath := filepath.Join(releaseDir, releaseID)

	imageDigest = func(string) (string, error) { return digest, nil }
	remoteImage = func(_ name.Reference) (v1.Image, error) { return empty.Image, nil }
	exportImage = func(_ v1.Image, w io.Writer) error {
		writeTar(t, w,
			tarEntry{name: ".deploy/post-activate", body: []byte("#!/bin/sh\nexit 9\n"), mode: 0o755, kind: tar.TypeReg},
			tarEntry{name: "fresh", body: []byte("ok"), mode: 0o644, kind: tar.TypeReg},
		)
		return nil
	}

	err := mgr.FetchAndLink("example.com/ns/app:latest", "current")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running post-activate hook")

	target, readErr := os.Readlink(filepath.Join(releaseDir, "current"))
	require.NoError(t, readErr)
	assert.Equal(t, releaseID, target)

	_, statErr := os.Stat(filepath.Join(releasePath, "fresh"))
	require.NoError(t, statErr)
}

func TestFetchAndLinkPreActivateFailureLeavesCurrentUnchanged(t *testing.T) {
	restore := stubReleaseDeps()
	defer restore()

	releaseDir := t.TempDir()
	currentLink := filepath.Join(releaseDir, "current")
	require.NoError(t, os.Symlink("previous", currentLink))

	mgr := New(
		WithReleaseDir(releaseDir),
		WithKeepLast(1),
		WithScriptRunsEnabled(time.Second),
	)

	const digest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	releasePath := filepath.Join(releaseDir, strings.Replace(digest, ":", "-", 1))

	imageDigest = func(string) (string, error) { return digest, nil }
	remoteImage = func(_ name.Reference) (v1.Image, error) { return empty.Image, nil }
	exportImage = func(_ v1.Image, w io.Writer) error {
		writeTar(t, w,
			tarEntry{name: ".deploy/pre-activate", body: []byte("#!/bin/sh\nexit 7\n"), mode: 0o755, kind: tar.TypeReg},
			tarEntry{name: "fresh", body: []byte("ok"), mode: 0o644, kind: tar.TypeReg},
		)
		return nil
	}

	err := mgr.FetchAndLink("example.com/ns/app:latest", "current")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "running pre-activate hook")

	target, readErr := os.Readlink(currentLink)
	require.NoError(t, readErr)
	assert.Equal(t, "previous", target)

	_, statErr := os.Stat(filepath.Join(releasePath, "fresh"))
	require.NoError(t, statErr)
}

func TestFetchAndLinkRunsHooksWhenActivatingExistingCompleteRelease(t *testing.T) {
	restore := stubReleaseDeps()
	defer restore()

	releaseDir := t.TempDir()
	mgr := New(
		WithReleaseDir(releaseDir),
		WithKeepLast(1),
		WithScriptRunsEnabled(time.Second),
	)

	const digest = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	releaseID := strings.Replace(digest, ":", "-", 1)
	releasePath := filepath.Join(releaseDir, releaseID)

	createExecutableScript(t, releasePath, "pre-activate", "#!/bin/sh\necho pre-existing > pre-existing\n")
	createExecutableScript(t, releasePath, "post-activate", "#!/bin/sh\necho post-existing > post-existing\n")
	require.NoError(t, os.WriteFile(filepath.Join(releasePath, releaseDoneMarker), nil, 0o600))

	imageDigest = func(string) (string, error) { return digest, nil }

	require.NoError(t, mgr.FetchAndLink("example.com/ns/app:latest", "current"))

	target, err := os.Readlink(filepath.Join(releaseDir, "current"))
	require.NoError(t, err)
	assert.Equal(t, releaseID, target)

	_, err = os.Stat(filepath.Join(releasePath, "pre-existing"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(releasePath, "post-existing"))
	require.NoError(t, err)
}

func TestFetchAndLinkRunsPreActivateAndLinksRelease(t *testing.T) {
	restore := stubReleaseDeps()
	defer restore()

	releaseDir := t.TempDir()
	mgr := New(
		WithReleaseDir(releaseDir),
		WithKeepLast(1),
		WithScriptRunsEnabled(time.Second),
	)

	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	releasePath := filepath.Join(releaseDir, strings.Replace(digest, ":", "-", 1))

	imageDigest = func(string) (string, error) { return digest, nil }
	remoteImage = func(_ name.Reference) (v1.Image, error) { return empty.Image, nil }
	exportImage = func(_ v1.Image, w io.Writer) error {
		writeTar(t, w,
			tarEntry{name: ".deploy/pre-activate", body: []byte("#!/bin/sh\necho pre-ran > pre-ran\n"), mode: 0o755, kind: tar.TypeReg},
			tarEntry{name: "fresh", body: []byte("ok"), mode: 0o644, kind: tar.TypeReg},
		)
		return nil
	}

	require.NoError(t, mgr.FetchAndLink("example.com/ns/app:latest", "current"))

	target, err := os.Readlink(filepath.Join(releaseDir, "current"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Base(releasePath), target)

	assert.Equal(t, "pre-ran\n", readTestFile(t, filepath.Join(releasePath, "pre-ran")))
}

func createExecutableScript(t *testing.T, releasePath, stage, body string) {
	t.Helper()

	scriptDir := filepath.Join(releasePath, ".deploy")
	require.NoError(t, os.MkdirAll(scriptDir, 0o750))
	scriptPath := filepath.Join(scriptDir, stage)
	require.NoError(t, os.WriteFile(scriptPath, []byte(body), 0o600))
	//#nosec:G302 -- Test fixture scripts must be executable to exercise hook execution.
	require.NoError(t, os.Chmod(scriptPath, 0o700))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	//#nosec:G304 -- Test helper reads files created in t.TempDir with fixed filenames.
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(content)
}
