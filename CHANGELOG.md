# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.1.2] - 2024-11-02

### Fixed

- Fixed taskListChannel resource leak in Close()
- Explicitly call contextCancel() on every task at Close() time
- Fixed multiple resource leaks in Cleanup()
- Add unit tests for Close() and Cleanup()

## [2.1.1] - 2024-10-31

### Fixed

- Close each task's response body when closing the task list

## [2.1.0] - 2024-10-31

### Changed

- Updated go-retryablehttp from v0.6.4 to v0.7.7
- Updated hms-base from v1.15.0 to v2.0.1

## [2.0.0] - 2022-01-07

### Changed

- Changed to use v2 of trs-kafkalib, requiring this package to become a v2 package as well.

## [1.6.3] - 2021-08-10

### Changed

- Upgrade dockerfile; add .github

## [1.6.2] - 2021-07-26

### Changed

- Converted to github and added design doc from internal site.


## [1.6.1] - 2021-07-22

### Changed

- Add support for building within the CSM Jenkins.

## [1.6.0] - 2021-06-28

### Security

- CASMHMS-4898 - Updated base container images for security updates.

## [1.5.3] - 2021-05-05

### Changed

- Updated docker-compose files to pull images from Artifactory instead of DTR.

## [1.5.2] - 2021-04-20

### Changed

- Updated Dockerfiles to pull base images from Artifactory instead of DTR.

## [1.5.1] - 2021-04-07

### Changed

- Removed setting artificial timeout on HTTP client.

## [1.5.0] - 2021-02-02

### Changed

- Updated Copyright/License info and re-vendor go packages

## [1.4.1] - 2021-01-20

### Added

- Added User-Agent headers to all  outbound HTTP operations.

## [1.4.0] - 2021-01-14

### Changed

- Updated license file.

## [1.3.4] - 2020-10-29

- CASMHMS-4148 - Update HMS vendor code for security fix.

## [1.3.3] - 2020-10-21

- CASMHMS-4105 - Updated base Golang Alpine image to resolve libcrypto vulnerability.

## [1.3.2] - 2020-10-07

- CASMHMS-4112 - Fixed backoff defaults for HTTP retries.

## [1.3.1] - 2020-08-12

- CASMHMS-2980 - Updated hms-trs-app-api to use the latest trusted baseOS images.

## [1.3.0] - 2020-07-23

- Added Ignore flag to task elements so the same list can be re-used but ignore previous failures.
- Added TLS certs and CA pool handling for HTTPS connections.

## [1.2.0] - 2020-07-01

- Added product field to jenkins file to enable building in the pipeline

## [1.1.6] - 2020-02-27

- Fixed clone

## [1.1.5] - 2020-02-27

- Protect createTaskArray from nil pointer in clone

## [1.1.4] - 2020-02-26

- Fix task list generation and add a mutex to client map

## [1.1.3] - 2020-02-24

- Added ability to ignore TLS certs.

## [1.1.2] - 2020-02-20

- Fixed nil pointer bug.

## [1.1.1] - 2020-02-20

- Updated kafkalib

## [1.1.0] - 2020-02-12

- Major refactor to the interface. Now using http.Request and http.Response directly.
  Removed direct references to kafka from Front side of interface.

## [1.0.2] - 2020-02-05

- Minor fixup to the HTTP API interface functions to fix inconsistencies.

## [1.0.1] - 2020-01-22

- Added guts to the API for local mode.  Remote/worker mode is not yet implemented.  This is enough functionality for applications to start using the TRS API.

## [1.0.0] - 2019-12-11

### Added

### Changed

### Deprecated

### Removed

### Fixed

### Security

