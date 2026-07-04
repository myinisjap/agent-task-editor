# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

To cut a release, add a `## [x.y.z] - YYYY-MM-DD` section below with the changes,
then push the matching tag (`git tag vx.y.z && git push origin vx.y.z`). The
release workflow builds and pushes the images and creates a GitHub Release using
this file's section for that version as the release notes.

## [Unreleased]

### Added
- Versioned multi-arch (amd64 + arm64) Docker images published to GHCR on every
  `v*` tag push, plus an automated GitHub Release.
- `docker-compose.release.yml` for running from the prebuilt
  `ghcr.io/myinisjap/agent-task-editor-{backend,frontend}` images instead of
  building from source.
- `run.sh` helper that injects the runtime env vars (repo mount, GitHub token,
  Claude auth, SSL bypass) and starts the stack from published images.
- This changelog.
