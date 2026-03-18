# Changelog

All notable changes to this project are documented in this file.

The format follows Keep a Changelog and uses semantic version tags (`vMAJOR.MINOR.PATCH`).

## [Unreleased]

### Added
- About menu in web console with app description, version status, update checker, and latest release changelog.
- CLI installer (`scripts/install.sh`) that installs from GitHub Releases in `auto`, `gui`, and `server` modes.
- About page now includes one-line updater command (`curl | bash`) for in-place updates.
- Added explicit mode-specific install one-liners (`auto`, `gui`, `server`, `update`) in README.

### Changed
- Release workflow now builds Linux/Windows for `amd64` and `arm64`.
- Linux GUI package names now include `_GUI`.
- Sidebar now shows current app version above logout.
- Installer now supports `--mode update`, auto-detects existing GUI/CLI install, and auto-manages `systemd` service for Linux server mode.
- Updater command now fetches installer from `scripts/install.sh` in repository (`raw.githubusercontent.com`) instead of release asset URL.

## [1.0.1] - 2026-03-18

### Added
- API traffic logging now includes resolved account detail fields.
- Version metadata injection during release builds via linker flags.

### Changed
- Improved API/logging behavior for streaming and full body capture.
- Improved settings UX and update status visibility.
