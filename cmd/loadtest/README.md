# Load Testing

This directory contains load testing tools for the scaleset-ec2-provider scaler with mocked AWS APIs.

## Quick Start

### Basic Load Test

```bash
make loadtest
```

This runs a simple load test with:
- 100 jobs
- 10 jobs/second rate
- Instant simulation mode

## Configuration Options


| Flag | Default | Description |
|------|---------|-------------|
| `-jobs` | 100 | Total number of jobs to simulate |
| `-rate` | 10 | Jobs per second to start |
| `-simulation` | instant | Simulation mode: `instant` or `realistic` |
| `-metrics-port` | 0 | Port for Prometheus metrics (0 = disabled) |
| `-concurrency` | 10 | Maximum concurrent runners |
| `-max-duration` | 10m | Maximum test duration |


## Simulation Modes

### Instant
Jobs complete immediately (0ms duration). Best for:
- High-throughput testing
- Fast CI execution
- Stress testing the scaler logic

```bash
./dist/loadtest -jobs=1000 -rate=50 -simulation=instant
```

### Realistic
Jobs have random durations (30s - 5m). Best for:
- Simulating real-world workloads
- Testing auto-scaling behavior
- Long-running performance tests

```bash
./dist/loadtest -jobs=50 -rate=5 -simulation=realistic -max-duration=15m
```

## Examples

### High-throughput test

```bash
./dist/loadtest -jobs=500 -rate=100 -concurrency=50
```

### Long-running realistic test

```bash
./dist/loadtest -jobs=200 -rate=2 -simulation=realistic -max-duration=1h
```

## Make Targets

```bash
make loadtest              # Basic instant load test
make loadtest-realistic    # Realistic simulation
```

## Architecture

The load test uses:
- **Mocked EC2 API** - Simulates instance creation/termination without real AWS calls
- **Mocked Scaleset API** - Simulates JIT config generation
- **Simulated Jobs** - Generates job started/completed events at configurable rate

This allows testing the scaler logic and metrics collection without external dependencies.

## Development

To build the load test binary:
```bash
make build-loadtest
```

The binary will be created at `dist/loadtest`.
