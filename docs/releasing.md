# Releasing cdp

The Release workflow builds archives for Linux, macOS and Windows on amd64 and
arm64. Linux/macOS archives are `.tar.gz`; Windows archives are `.zip`. Each
contains the standalone CLI, README, specification and usage documentation.
`SHA256SUMS` contains the SHA-256 digests of the six archives.

Every pull request builds these archives and uploads a `release-archives` Actions
artifact. This validates the packaging before a version is tagged. Normal CI also
runs cross-platform tests/builds, lint, race detection and blank-profile browser
acceptance tests.

## Prepare and publish

1. Set `internal/protocol/protocol.go`'s `Version` to the intended semantic version
   and update user-facing documentation in a pull request.
2. Wait for the Go and Release workflows to pass and merge the pull request.
3. From the intended main commit, create and push the matching version tag:

   ```sh
   git tag -a v1.0.0 -m "cdp v1.0.0"
   git push origin v1.0.0
   ```

4. The tag triggers the Release workflow. It rejects tags that do not match
   `cdp version`, then attaches all archives and checksums to a **draft** GitHub
   release with generated notes.
5. Confirm both workflows passed for the tagged commit, review the notes and
   assets, and publish the draft through GitHub.

Creating a pull request does not create a tag or a public release. For a future
release, replace `1.0.0` in the example with that release's version.

## Verify an archive

Download the archive and `SHA256SUMS` from the same release. On Linux:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

On macOS, compare `shasum -a 256 ARCHIVE` with its line in `SHA256SUMS`. On Windows,
compare `(Get-FileHash ARCHIVE -Algorithm SHA256).Hash` with the listed digest.
Extract the matching archive and run `cdp version` (`.\cdp.exe version` in
PowerShell) to confirm the expected version.
