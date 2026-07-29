# Releasing AI Tracker

Releases are built by GitHub Actions from an annotated `v*` tag. The workflow tests, builds, race-tests, vets, and lints before GoReleaser receives write access to publish a release.

## Checklist

1. Start from a clean, current `main` branch and choose the next semantic version.
2. Update user-facing installation examples and migration notes.
3. Run the local gates:

   ```bash
   mise run test
   go test -race ./...
   go vet ./...
   mise run lint
   scripts/test-ait-wrapper.sh
   goreleaser check
   goreleaser release --snapshot --clean
   scripts/verify-release-archives.sh dist
   ```

4. Verify at least one snapshot archive against `checksums.txt`, then extract it and run both `ait version` and `ai-tracker version`.
5. Commit and push `main`, create an annotated semantic-version tag such as `vX.Y.Z`, and push the tag.
6. Watch the `Release` workflow until both `verify` and `release` succeed.
7. Verify the published `checksums.txt`, Linux and macOS archives, release notes, and embedded version.
8. Test a clean managed install:

   ```bash
   mise use -g github:spencer-life/ai-tracker@vX.Y.Z
   mise reshim
   mise which ait
   mise which ai-tracker
   ait version
   ai-tracker version
   ```

Do not create a release manually before the workflow finishes. Linux/WSL and macOS x86-64/ARM64 are the supported release targets; Windows users run the Linux binary under WSL.
