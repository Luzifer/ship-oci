package unpack

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamedTarRejectsPathEscape(t *testing.T) {
	destDir := t.TempDir()
	payload := tarBytes(t, tarEntry{
		name: "../escape",
		body: []byte("nope"),
		mode: 0o644,
		kind: tar.TypeReg,
	})

	err := StreamedTar(bytes.NewReader(payload), destDir)
	require.Error(t, err)
}

func TestStreamedTarRejectsUnsafeSymlink(t *testing.T) {
	destDir := t.TempDir()
	payload := tarBytes(t, tarEntry{
		name:     "link",
		linkname: "../../escape",
		mode:     0o777,
		kind:     tar.TypeSymlink,
	})

	err := StreamedTar(bytes.NewReader(payload), destDir)
	require.Error(t, err)
}

func TestStreamedTarRejectsWriteThroughSymlink(t *testing.T) {
	destDir := t.TempDir()
	require.NoError(t, os.Symlink("/tmp", filepath.Join(destDir, "link")))

	payload := tarBytes(t, tarEntry{
		name: "link/file",
		body: []byte("nope"),
		mode: 0o644,
		kind: tar.TypeReg,
	})

	err := StreamedTar(bytes.NewReader(payload), destDir)
	require.Error(t, err)
}

func TestStreamedTarPreservesModes(t *testing.T) {
	destDir := t.TempDir()
	payload := tarBytes(t,
		tarEntry{name: "bin", mode: 0o700, kind: tar.TypeDir},
		tarEntry{name: "bin/run", body: []byte("#!/bin/sh\n"), mode: 0o755, kind: tar.TypeReg},
		tarEntry{name: "config", body: []byte("data"), mode: 0o644, kind: tar.TypeReg},
	)

	require.NoError(t, StreamedTar(bytes.NewReader(payload), destDir))

	for _, tc := range []struct {
		path string
		want os.FileMode
	}{
		{path: filepath.Join(destDir, "bin"), want: 0o700},
		{path: filepath.Join(destDir, "bin", "run"), want: 0o755},
		{path: filepath.Join(destDir, "config"), want: 0o644},
	} {
		st, err := os.Stat(tc.path)
		require.NoError(t, err)
		assert.Equal(t, tc.want, st.Mode().Perm(), "%q mode mismatch", tc.path)
	}
}

func TestStreamedTarAppliesDirectoryModeAfterImplicitParentCreation(t *testing.T) {
	destDir := t.TempDir()
	payload := tarBytes(t,
		tarEntry{name: "bin/run", body: []byte("#!/bin/sh\n"), mode: 0o755, kind: tar.TypeReg},
		tarEntry{name: "bin", mode: 0o700, kind: tar.TypeDir},
	)

	require.NoError(t, StreamedTar(bytes.NewReader(payload), destDir))

	st, err := os.Stat(filepath.Join(destDir, "bin"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), st.Mode().Perm(), "%q mode mismatch", filepath.Join(destDir, "bin"))
}

type tarEntry struct {
	name     string
	linkname string
	body     []byte
	mode     int64
	kind     byte
}

func tarBytes(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, entry := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     entry.name,
			Linkname: entry.linkname,
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

	return buf.Bytes()
}
