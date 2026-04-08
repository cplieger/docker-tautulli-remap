# Changelog

## 2026.04.07-8d3d8c9 (2026-04-08)

### Changed

- Update Go toolchain configuration

### Dependencies

- Update go to v1.26.2 (#172)
- Update golang:1.26-alpine docker digest to c2a1f7b (#175)

## 2026.04.01-c71639f (2026-04-01)

### Added

- Enhance security with API key sanitization in error messages
- Test(tautulli-remap): add property-based and edge case tests
- Test(tautulli-remap): add comprehensive HTTP and API integration tests
- Add input validation and resource exhaustion protections
- Test(apps): add comprehensive test coverage for identity loading and cert conversion
- Add fallback matching strategies and improve service dependencies
- Add retry logic and request pacing for API calls
- Add flexible JSON type handling for numeric fields
- Migrate metadata fixer from Python to Go with improved matching

### Fixed

- Improve startup health state and refactor history processing
- Handle strconv.Atoi errors explicitly

### Changed

- Refactor(tautulli-remap): extract media type string constants
- Style(tautulli-remap): align variable declarations and simplify switch statements
- Refactor(tautulli-remap): reorganize code structure and improve error handling
- Migrate to structured logging and enhance context handling
- Refactor(tautulli-remap): extract GUID normalization mappings into table
- Refactor(apps): extract method identifiers to constants and improve deployment docs formatting
- Test(apps): refactor test structure and improve helper functions
- Update health checks and standardize environment variable quoting
- Update encrypted environment files across all services
- Docs(steering): Document operational constraints and infrastructure patterns
- Hardcode health probe port to 9147
- Update to prebuilt image with pinned digest

### Dependencies

- Update gcr.io/distroless/static-debian13:nonroot docker digest to e3f9456 (#138)
- Update third-party dependencies

## 2026.03.21-4ca7dd4 (2026-03-22)

### Added

- Enhance security with API key sanitization in error messages

## 2026.03.15-e917ce6 (2026-03-16)

### Dependencies

- Update gcr.io/distroless/static-debian13:nonroot docker digest to e3f9456 (#138)

## 2026.03.14-558eb98 (2026-03-14)

### Added

- Test(tautulli-remap): add property-based and edge case tests
- Test(tautulli-remap): add comprehensive HTTP and API integration tests

### Changed

- Refactor(tautulli-remap): extract media type string constants
- Style(tautulli-remap): align variable declarations and simplify switch statements

## 2026.03.12-4ccec15 (2026-03-12)

### Fixed

- Improve startup health state and refactor history processing

## 2026.03.11-c669732 (2026-03-11)

### Changed

- Refactor(tautulli-remap): reorganize code structure and improve error handling
- Migrate to structured logging and enhance context handling

## 2026.03.07-72ffdd8 (2026-03-08)

### Added

- Add input validation and resource exhaustion protections

### Changed

- Extract GUID normalization mappings into table

## 2026.03.07-9112d85 (2026-03-07)

### Added

- Minor healthcheck code improvements and optimizations

## 2026.03.06-d471f34 (2026-03-07)

### Fixed

- Handle strconv.Atoi errors explicitly

## 2026.03.06-fef2c0b (2026-03-06)

### Changed

- Update Go toolchain and builder image

## 2026.03.05-479822c (2026-03-05)

### Changed

- Extract method identifiers to constants and improve deployment docs formatting
- Refactor test structure and improve helper functions
- Add comprehensive test coverage for identity loading and cert conversion

## 2026.03.03-cdb462e (2026-03-04)

- Initial release
