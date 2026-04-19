package release

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"git.luzifer.io/luzifer/ship-oci/pkg/scriptrunner"
	"git.luzifer.io/luzifer/ship-oci/pkg/unpack"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sirupsen/logrus"
)

const (
	defaultKeepLast   = 3
	releaseRootMode   = 0o750
	releaseMarkerMode = 0o600
	releaseDoneMarker = ".complete"
)

type (
	// Manager fetches OCI image releases, links the current release,
	// and prunes old snapshots.
	Manager struct {
		keepLast         int
		releaseDir       string
		scriptRunEnabled bool
		scriptRunTimeout time.Duration
	}

	// Opt configures a Manager.
	Opt func(m *Manager)

	resolvedRelease struct {
		id       string
		digest   string
		dir      string
		marker   string
		imageRef name.Reference
	}
)

var (
	// test-wrapper, overwritten in tests
	exportImage    = crane.Export
	imageDigest    = func(ref string) (string, error) { return crane.Digest(ref) }
	parseReference = name.ParseReference
	remoteImage    = func(ref name.Reference) (v1.Image, error) { return remote.Image(ref) }
)

// New creates a Manager with the provided options.
func New(opts ...Opt) (m Manager) {
	m.keepLast = defaultKeepLast

	for _, o := range opts {
		o(&m)
	}

	return m
}

// WithKeepLast configures how many release directories are retained
// during pruning.
func WithKeepLast(n int) Opt {
	return func(m *Manager) { m.keepLast = n }
}

// WithReleaseDir configures the directory used to store release
// snapshots and symlinks.
func WithReleaseDir(rd string) Opt {
	return func(m *Manager) { m.releaseDir = rd }
}

// WithScriptRunsEnabled enables execution of .deploy/pre-activate and
// .deploy/post-activate scripts from the target release directory. The
// provided timeout is applied to each hook run individually.
func WithScriptRunsEnabled(scriptTimeout time.Duration) Opt {
	return func(m *Manager) {
		m.scriptRunEnabled = true
		m.scriptRunTimeout = scriptTimeout
	}
}

// FetchAndLink downloads the referenced image, unpacks it into a
// release directory, and updates the current symlink.
func (m Manager) FetchAndLink(imageRef, currentName string) (err error) {
	if err = m.validate(); err != nil {
		return err
	}

	currentLink := filepath.Join(m.releaseDir, currentName)
	log := logrus.WithField("image_ref", imageRef)
	log.Info("fetching release")

	if err = ensureReleaseRoot(m.releaseDir); err != nil {
		return err
	}

	releaseMeta, err := resolveRelease(imageRef, m.releaseDir)
	if err != nil {
		return err
	}

	log = log.WithFields(logrus.Fields{
		"digest":     releaseMeta.digest,
		"release_id": releaseMeta.id,
	})
	log.Info("resolved release image")

	if shouldReuseCurrentLink(currentLink, releaseMeta.dir, releaseMeta.marker) {
		log.Info("skipping fetch: current release already active")
		return nil
	}

	if !isCompleteRelease(releaseMeta.marker) {
		log.Info("downloading and unpacking release")

		if err = resetIncompleteRelease(releaseMeta.dir); err != nil {
			return err
		}

		if err = stageRelease(releaseMeta); err != nil {
			return err
		}
	} else {
		log.Info("skipping extraction: release already complete")
	}

	if m.scriptRunEnabled {
		logrus.WithFields(logrus.Fields{
			"hook":       "pre-activate",
			"release_id": releaseMeta.id,
		}).Info("running release hook")

		if err = scriptrunner.Run(releaseMeta.dir, "pre-activate", m.scriptRunTimeout); err != nil {
			return fmt.Errorf("running pre-activate hook: %w", err)
		}
	} else {
		log.Info("skipping hooks: disabled")
	}

	log.WithField("release_id", releaseMeta.id).Info("linking current release")
	if err = linkCurrentRelease(currentLink, releaseMeta.id); err != nil {
		return err
	}

	if m.scriptRunEnabled {
		logrus.WithFields(logrus.Fields{
			"hook":       "post-activate",
			"release_id": releaseMeta.id,
		}).Info("running release hook")

		if err = scriptrunner.Run(releaseMeta.dir, "post-activate", m.scriptRunTimeout); err != nil {
			return fmt.Errorf("running post-activate hook: %w", err)
		}
	}

	return nil
}

// PruneOld removes old release directories while keeping the most
// recent configured snapshots.
func (m Manager) PruneOld() (err error) {
	if err := m.validate(); err != nil {
		return err
	}

	log := logrus.WithField("keep_last", m.keepLast)
	log.Info("pruning old releases")

	ents, err := os.ReadDir(m.releaseDir)
	if err != nil {
		return fmt.Errorf("enumerating release dir: %w", err)
	}

	type item struct {
		path string
		mod  int64
	}
	var items []item

	for _, e := range ents {
		if !e.IsDir() {
			continue
		}

		p := filepath.Join(m.releaseDir, e.Name())
		st, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("getting stat for snapshot %q: %w", e.Name(), err)
		}

		items = append(items, item{path: p, mod: st.ModTime().Unix()})
	}

	if len(items) <= m.keepLast {
		log.Info("nothing to prune")
		return nil
	}

	sort.Slice(items, func(i, j int) bool { return items[i].mod > items[j].mod })

	for _, old := range items[m.keepLast:] {
		logrus.WithField("release_id", filepath.Base(old.path)).Info("pruning old release")
		if err := os.RemoveAll(old.path); err != nil {
			return fmt.Errorf("pruning old snapshot: %w", err)
		}
	}

	return nil
}

func ensureReleaseRoot(releaseDir string) error {
	if err := os.MkdirAll(releaseDir, releaseRootMode); err != nil {
		return fmt.Errorf("ensuring release dir: %w", err)
	}

	return nil
}

func extractRelease(ref name.Reference, targetDir string) error {
	img, err := remoteImage(ref)
	if err != nil {
		return fmt.Errorf("creating remote: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		if err := exportImage(img, pw); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		_ = pw.Close()
	}()

	if err := unpack.StreamedTar(pr, targetDir); err != nil {
		return fmt.Errorf("unpacking streamed image: %w", err)
	}

	return nil
}

func isCompleteRelease(markerPath string) bool {
	st, err := os.Stat(markerPath)
	return err == nil && !st.IsDir()
}

func linkCurrentRelease(currentLink, releaseID string) error {
	newLink := currentLink + ".new"

	if err := os.Remove(newLink); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing temp symlink: %w", err)
	}

	if err := os.Symlink(releaseID, newLink); err != nil {
		return fmt.Errorf("creating temp symlink: %w", err)
	}

	if err := os.Rename(newLink, currentLink); err != nil {
		return fmt.Errorf("moving symlink into place: %w", err)
	}

	return nil
}

func markReleaseComplete(dir string) error {
	markerPath := filepath.Join(dir, releaseDoneMarker)

	//#nosec G304 -- markerPath is always derived from the managed temporary release directory.
	f, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, releaseMarkerMode)
	if err != nil {
		return fmt.Errorf("creating release marker: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("closing release marker: %w", err)
	}

	return nil
}

func resetIncompleteRelease(releaseDir string) error {
	if err := os.RemoveAll(releaseDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing incomplete release dir: %w", err)
	}

	return nil
}

func resolveRelease(imageRef, releaseRoot string) (resolvedRelease, error) {
	ref, err := parseReference(imageRef)
	if err != nil {
		return resolvedRelease{}, fmt.Errorf("parsing image ref: %w", err)
	}

	digest, err := imageDigest(imageRef)
	if err != nil {
		return resolvedRelease{}, fmt.Errorf("getting image digest: %w", err)
	}

	immutableRef, err := name.NewDigest(fmt.Sprintf("%s@%s", ref.Context().Name(), digest))
	if err != nil {
		return resolvedRelease{}, fmt.Errorf("building immutable image ref: %w", err)
	}

	releaseID := strings.Replace(digest, ":", "-", 1)
	releaseDir := filepath.Join(releaseRoot, releaseID)

	return resolvedRelease{
		id:       releaseID,
		digest:   digest,
		dir:      releaseDir,
		marker:   filepath.Join(releaseDir, releaseDoneMarker),
		imageRef: immutableRef,
	}, nil
}

func resolveSymlinkTarget(linkPath, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}

	return filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
}

func shouldReuseCurrentLink(currentLink, releaseDir, releaseMarker string) bool {
	cur, err := os.Readlink(currentLink)
	if err != nil {
		return false
	}

	return resolveSymlinkTarget(currentLink, cur) == filepath.Clean(releaseDir) && isCompleteRelease(releaseMarker)
}

func stageRelease(release resolvedRelease) (err error) {
	tmpDir, err := os.MkdirTemp(filepath.Dir(release.dir), release.id+".tmp-")
	if err != nil {
		return fmt.Errorf("creating temp release dir: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err := extractRelease(release.imageRef, tmpDir); err != nil {
		return err
	}

	if err := markReleaseComplete(tmpDir); err != nil {
		return err
	}

	if err := os.Rename(tmpDir, release.dir); err != nil {
		if isCompleteRelease(release.marker) {
			return nil
		}

		return fmt.Errorf("publishing release dir: %w", err)
	}

	return nil
}

func (m Manager) validate() error {
	if m.keepLast < 1 {
		return fmt.Errorf("keep-last must be >= 1")
	}

	if m.releaseDir == "" {
		return fmt.Errorf("release dir must not be empty")
	}

	return nil
}
