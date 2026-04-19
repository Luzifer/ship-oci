# ship-oci

`ship-oci` is a small deployment helper for OCI images.

It pulls an image, resolves it to an immutable digest, extracts the image contents into a release directory, updates a `current` symlink, and prunes older releases. It is designed for simple host-based deployments where an OCI image is used as the release artifact.

## How It Works

For each run, `ship-oci`:

1. Resolves the requested image reference to its digest.
2. Uses that digest as the immutable release identifier.
3. Extracts the image into a release directory named after the digest.
4. Marks the release complete.
5. Optionally runs hook scripts from the new release.
6. Atomically repoints `current` to the new release.
7. Prunes older releases, keeping only the configured number.

If the same digest is already active and complete, the release is reused instead of being extracted again.

## Release Layout

By default releases are stored below `./releases`:

```text
releases/
  current -> sha256-012345...
  sha256-012345.../
    .complete
    ...
```

Each extracted release directory is immutable in practice and keyed by digest. The `current` symlink points at the active release.

## Hooks

Hooks are optional and disabled by default.

When enabled with `--run-hooks`, `ship-oci` looks for these files inside the new release:

- `.deploy/pre-activate`
- `.deploy/post-activate`

Hook behavior:

- Hooks run on the host, not inside a container.
- Hooks run with their working directory set to the release directory.
- Hooks inherit the parent process environment.
- `pre-activate` runs before `current` is updated.
- `post-activate` runs after `current` is updated.
- Each hook run gets its own timeout configured by `--hook-timeout`.
- Missing hook files are ignored.

Important operational note:

- If `pre-activate` fails, activation is aborted and `current` is left unchanged.
- If `post-activate` fails, `ship-oci` returns an error but does not roll back `current`.

That last point is intentional: a failed `post-activate` means the new release may already be live.

## Security Notes

`ship-oci` includes a few safety properties:

- Image references are resolved to immutable digests before extraction.
- Extraction uses guarded path handling to reject tar path escapes and unsafe symlinks.
- Release publication uses a temporary directory plus rename.
- Hooks are opt-in via `--run-hooks=false` by default.

Hook execution is the main trust boundary:

- Enabling hooks means code shipped inside the image can execute on the host.
- Only enable hooks for trusted images and trusted registries.

## Usage

Basic deployment:

```bash
ship-oci --image-ref ghcr.io/example/app:latest
```

Deploy to a custom release directory and keep five releases:

```bash
ship-oci \
  --image-ref ghcr.io/example/app:latest \
  --release-dir /srv/myapp/releases \
  --keep-last 5
```

Enable hooks with a custom timeout:

```bash
ship-oci \
  --image-ref ghcr.io/example/app:latest \
  --run-hooks \
  --hook-timeout 30s
```

## Flags

- `--image-ref`, `-i`: Image reference to pull.
- `--release-dir`, `-r`: Directory containing extracted releases and the `current` symlink. Default: `releases`
- `--keep-last`: Number of release directories to retain. Default: `3`
- `--run-hooks`: Enable `.deploy/pre-activate` and `.deploy/post-activate` execution. Default: `false`
- `--hook-timeout`: Maximum runtime for each hook execution. Default: `1m`
- `--log-level`: Log level for the binary. Default: `info`
- `--version`: Print the version and exit.

Environment variables are also supported through `rconfig`.

## Build

```bash
go build ./...
```

Or build the binary directly:

```bash
go build -o ship-oci .
```

## Practical Notes

- `keep-last` must be at least `1`.
- Pruning only removes release directories, not the `current` symlink.
- Hook scripts can use relative paths because they run with the release directory as their current working directory.
- If extraction fails for a release, the incomplete release directory is cleaned up before retry.
