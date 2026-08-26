# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Fix SQLITE_BUSY errors under high-concurrency monitor sampling**
  - Add `Store.writeMu` mutex to serialize write operations and prevent database contention
  - Apply write lock to `AddMetric` to protect concurrent metric inserts
  - Limit concurrent sampling goroutines to 5 using a semaphore with context cancellation support
  - Ensure semaphore acquisition respects context cancellation to prevent goroutine leaks during shutdown

- **Prevent WAL file bloat with periodic checkpoint**
  - Add `Store.CheckpointWAL()` method using `PRAGMA wal_checkpoint(TRUNCATE)`
  - Checkpoint returns `(busy, error)` to properly detect when checkpoint is blocked
  - Run WAL checkpoint every 10 minutes in the monitor manager's main loop
  - Log when checkpoint is blocked (busy=1) for observability

### Changed

- Monitor sampling now uses bounded concurrency (max 5 parallel samples) instead of unlimited goroutines
- `Store.CheckpointWAL()` signature changed from `error` to `(bool, error)` to report busy status
