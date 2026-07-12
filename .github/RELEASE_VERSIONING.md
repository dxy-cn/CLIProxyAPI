# DXY Release Versioning

`dxy-cn/CLIProxyAPI` releases identify both the synchronized upstream baseline and the local release revision.

## Format

```text
v<upstream-version>-dxy.<revision>
```

Examples:

```text
v7.2.70-dxy.1  # first DXY release based on upstream v7.2.70
v7.2.70-dxy.2  # additional local change on the same upstream baseline
v7.2.71-dxy.1  # first DXY release after synchronizing upstream v7.2.71
```

Rules:

1. The upstream tag must exist and be an ancestor of the release commit.
2. The first release for an upstream baseline uses `dxy.1`.
3. Additional local releases on the same baseline increment the revision.
4. Synchronizing a new upstream tag resets the local revision to `dxy.1`.
5. A release tag and its OCI image are immutable. Rebuilding the same source does not create a new release version.
6. Historical tags such as `v9.1.18-sol` remain unchanged. This rule applies to new releases only.

The suffix is a SemVer prerelease identifier. Version ordering is only guaranteed within the DXY release channel; do not compare DXY and upstream tags as one upgrade stream.

## Release Identity

Each release records four identities:

```text
Git tag:    v7.2.70-dxy.1
Git commit: full immutable commit SHA
OCI tag:    7.2.70-dxy.1
OCI digest: sha256:...
```

Production deployment must use the OCI digest. Tags are human-readable release and audit identifiers.

## Automation

- `.github/scripts/validate-release-version.sh` validates the tag format, upstream ancestry, and checked-out commit.
- `.github/workflows/release.yaml` only builds existing `v*-dxy.*` tags.
- `.github/workflows/docker-image.yml` publishes only the exact release or test tag plus `sha-*`.
- Release builds do not automatically publish `major`, `major.minor`, or `latest` tags.
- Moving a production alias such as `latest` requires a separate, explicit production promotion process.
