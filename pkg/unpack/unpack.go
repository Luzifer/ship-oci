package unpack

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxEntrySize  int64 = 1 << 30 // 1 GiB
	dirCreateMode       = 0o750
)

// StreamedTar extracts a streamed tar archive into destDir.
func StreamedTar(src io.Reader, destDir string) (err error) {
	tr := tar.NewReader(src)
	cleanDestDir := filepath.Clean(destDir)

	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}

			return fmt.Errorf("reading tar entry: %w", err)
		}

		if err := extractTarEntry(tr, cleanDestDir, h); err != nil {
			return err
		}
	}
}

func extractTarEntry(src io.Reader, destDir string, header *tar.Header) error {
	cleanTarget, err := safeExtractPath(destDir, header.Name)
	if err != nil {
		return err
	}

	if err := ensureNoSymlinkTraversal(destDir, cleanTarget); err != nil {
		return err
	}

	switch header.Typeflag {
	case tar.TypeDir:
		return createDirectory(cleanTarget, header.Mode)
	case tar.TypeReg:
		return createRegularFile(src, cleanTarget, header.Mode, header.Size)
	case tar.TypeSymlink:
		return createSymlink(destDir, cleanTarget, header.Linkname)
	default:
		return nil
	}
}

func createDirectory(target string, mode int64) error {
	if err := os.MkdirAll(target, dirModeFromHeader(mode)); err != nil {
		return fmt.Errorf("creating directory %q: %w", target, err)
	}

	if err := os.Chmod(target, dirModeFromHeader(mode)); err != nil {
		return fmt.Errorf("setting directory mode for %q: %w", target, err)
	}

	return nil
}

func createRegularFile(src io.Reader, target string, mode, size int64) error {
	if err := os.MkdirAll(filepath.Dir(target), dirCreateMode); err != nil {
		return fmt.Errorf("creating parent directory for %q: %w", target, err)
	}

	return writeRegularFile(src, target, mode, size)
}

func createSymlink(destDir, target, linkTarget string) error {
	if _, err := safeSymlinkTarget(destDir, target, linkTarget); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target), dirCreateMode); err != nil {
		return fmt.Errorf("creating parent directory for symlink %q: %w", target, err)
	}

	if err := os.Symlink(linkTarget, target); err != nil {
		return fmt.Errorf("creating symlink %q: %w", target, err)
	}

	return nil
}

func safeExtractPath(destDir, entryName string) (cleanTarget string, err error) {
	cleanTarget = filepath.Clean(filepath.Join(destDir, entryName))
	cleanDestWithSep := destDir + string(os.PathSeparator)

	if cleanTarget == destDir || strings.HasPrefix(cleanTarget, cleanDestWithSep) {
		return cleanTarget, nil
	}

	return "", fmt.Errorf("tar path escape: %s", entryName)
}

func safeSymlinkTarget(destDir, linkPath, linkTarget string) (string, error) {
	resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(linkPath), linkTarget))
	cleanDestWithSep := destDir + string(os.PathSeparator)

	if resolvedTarget == destDir || strings.HasPrefix(resolvedTarget, cleanDestWithSep) {
		return resolvedTarget, nil
	}

	return "", fmt.Errorf("tar symlink escape: %s -> %s", linkPath, linkTarget)
}

func ensureNoSymlinkTraversal(destDir, target string) error {
	relPath, err := filepath.Rel(destDir, target)
	if err != nil {
		return fmt.Errorf("getting relative path for %q: %w", target, err)
	}

	cur := destDir
	for _, part := range strings.Split(relPath, string(os.PathSeparator)) {
		if part == "." || part == "" {
			continue
		}

		cur = filepath.Join(cur, part)

		st, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return fmt.Errorf("lstat %q: %w", cur, err)
		}

		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tar symlink traversal: %s", cur)
		}
	}

	return nil
}

func writeRegularFile(src io.Reader, target string, mode, size int64) (err error) {
	if size < 0 {
		return fmt.Errorf("negative file size for %q: %d", target, size)
	}

	if size > maxEntrySize {
		return fmt.Errorf("file %q exceeds max allowed size: %d", target, size)
	}

	//#nosec G304 -- target is constrained by safeExtractPath before reaching this helper.
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileModeFromHeader(mode))
	if err != nil {
		return fmt.Errorf("opening file %q: %w", target, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("closing file %q: %w", target, closeErr)
		}
	}()

	if _, err := io.CopyN(f, src, size); err != nil {
		return fmt.Errorf("writing file %q: %w", target, err)
	}

	return nil
}

func dirModeFromHeader(mode int64) os.FileMode {
	return fileModeFromHeader(mode)
}

func fileModeFromHeader(mode int64) os.FileMode {
	if mode < 0 {
		mode = 0
	}

	return os.FileMode(mode) & os.ModePerm
}
