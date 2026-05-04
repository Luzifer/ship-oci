package release

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1" //revive:disable-line:redundant-import-alias // enforced by goimports
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	tarEntry struct {
		name string
		body []byte
		mode int64
		kind byte
	}
)

func TestFetchAndLinkUsesImmutableDigestRefAndCleansFailedTempRelease(t *testing.T) {
	restore := stubReleaseDeps()
	defer restore()

	releaseDir := t.TempDir()
	mgr := New(WithReleaseDir(releaseDir), WithKeepLast(1))

	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	imageDigest = func(string) (string, error) { return digest, nil }

	var fetchedRef string
	remoteImage = func(ref name.Reference) (v1.Image, error) {
		fetchedRef = ref.String()
		return empty.Image, nil
	}

	exportImage = func(_ v1.Image, w io.Writer) error {
		tw := tar.NewWriter(w)
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "stale",
			Mode:     0o644,
			Size:     int64(len("bad")),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte("bad"))
		require.NoError(t, err)

		return errors.New("boom")
	}

	err := mgr.FetchAndLink("example.com/ns/app:latest", "current")
	require.Error(t, err)

	releasePath := filepath.Join(releaseDir, "sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	_, statErr := os.Stat(releasePath)
	require.True(t, os.IsNotExist(statErr), "expected no published release dir, got %v", statErr)

	exportImage = func(_ v1.Image, w io.Writer) error {
		writeTar(t, w, tarEntry{name: "fresh", body: []byte("ok"), mode: 0o644, kind: tar.TypeReg})
		return nil
	}

	require.NoError(t, mgr.FetchAndLink("example.com/ns/app:latest", "current"))

	assert.Equal(t, "example.com/ns/app@"+digest, fetchedRef)

	_, err = os.Stat(filepath.Join(releasePath, "fresh"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(releasePath, "stale"))
	require.True(t, os.IsNotExist(err), "expected no stale file, got %v", err)

	_, err = os.Stat(filepath.Join(releasePath, releaseDoneMarker))
	require.NoError(t, err)

	target, err := os.Readlink(filepath.Join(releaseDir, "current"))
	require.NoError(t, err)
	assert.Equal(t, "sha256-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", target)
}

func TestPruneOldNoopWhenSnapshotsDoNotExceedKeepLast(t *testing.T) {
	releaseDir := t.TempDir()
	mgr := New(WithReleaseDir(releaseDir), WithKeepLast(3))

	for _, dir := range []string{"one", "two"} {
		require.NoError(t, os.Mkdir(filepath.Join(releaseDir, dir), 0o750))
	}

	require.NoError(t, mgr.PruneOld())

	for _, dir := range []string{"one", "two"} {
		_, err := os.Stat(filepath.Join(releaseDir, dir))
		assert.NoError(t, err)
	}
}

func TestPruneOldRejectsInvalidKeepLast(t *testing.T) {
	for _, keepLast := range []int{0, -1} {
		mgr := New(WithReleaseDir(t.TempDir()), WithKeepLast(keepLast))
		assert.Error(t, mgr.PruneOld(), "expected error for keepLast=%d", keepLast)
	}
}

func stubReleaseDeps() func() {
	oldDigest := imageDigest
	oldParse := parseReference
	oldRemote := remoteImage
	oldExport := exportImage

	parseReference = name.ParseReference

	return func() {
		imageDigest = oldDigest
		parseReference = oldParse
		remoteImage = oldRemote
		exportImage = oldExport
	}
}

func writeTar(t *testing.T, w io.Writer, entries ...tarEntry) {
	t.Helper()

	tw := tar.NewWriter(w)
	for _, entry := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.body)),
			Typeflag: entry.kind,
		}))

		if len(entry.body) > 0 {
			_, err := tw.Write(entry.body)
			require.NoError(t, err)
		}
	}

	require.NoError(t, tw.Close())
}
