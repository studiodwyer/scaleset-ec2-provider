# scaleset-ec2-provider

EC2 provider for [actions/scaleset](https://github.com/actions/scaleset) — scale GitHub Actions runners using EC2 instances. Implements ephemeral runners that execute a single job and self-terminate.

Configuration is provided via a TOML file (`config.toml`). See [`config.example.toml`](config.example.toml) for a fully-commented template.

```bash
make build

./dist/scaleset-ec2-provider run --config config.toml
```

To override individual values from the config file on a single run, the only flags accepted by `run` are `--config` (required), `--log-level`, `--log-format`, and `--version`.

If the binary crashed before cleanup, you can manually delete a scale set (uses the same config file, with any flags overriding their counterparts):

```bash
./dist/scaleset-ec2-provider delete --config config.toml
```

## Configuration

Config is loaded from the TOML file passed to `--config`. Lists (`labels`, `instance_types`, `security_group_ids`) are native TOML arrays. The GitHub App private key is a multi-line PEM string using TOML triple-quoted strings.

| Key | Required | Description |
|-----|----------|-------------|
| `url` | Yes | GitHub org, repo, or enterprise URL |
| `name` | Yes | Scale set name |
| `labels` | No | Labels for workflow targeting (defaults to `name`) |
| `max_runners` | No | Maximum concurrent runners (default: 10) |
| `min_runners` | No | Minimum idle runners (default: 0) |
| `runner_group` | No | Runner group name (default: default runner group) |
| `token` | Cond.* | Personal Access Token |
| `[github_app]` | Cond.* | GitHub App credentials block (see below) |
| `ami_id` | Yes | AMI ID with runner pre-installed |
| `instance_types` | No | EC2 instance types, shuffled on each launch (default: `["t3.medium"]`) |
| `subnet_id` | Yes | Subnet ID for instances |
| `security_group_ids` | Yes | Security group IDs |
| `iam_instance_profile` | No | IAM instance profile name |
| `key_name` | No | SSH key pair name |
| `spot` | No | Use EC2 Spot instances instead of on-demand (default: false) |
| `metrics_port` | No | Port for Prometheus metrics endpoint (default: 0/disabled) |
| `region` | No | AWS region for metrics labels (auto-detected if not specified) |
| `log_level` | No | `debug`, `info`, `warn`, `error` (default: `info`) |
| `log_format` | No | `text`, `json` (default: `text`) |

*Provide either `[github_app]` (all three fields) OR `token`.

### `[github_app]`

| Key | Description |
|-----|-------------|
| `client_id` | GitHub App Client ID |
| `installation_id` | GitHub App Installation ID |
| `private_key` | GitHub App private key (PEM, triple-quoted multi-line string) |

### Example

```toml
url = "https://github.com/myorg/myrepo"
name = "ec2-runners"
labels = ["ec2-runner"]
ami_id = "ami-1234567890abcdef0"
instance_types = ["t3.medium"]
subnet_id = "subnet-12345678"
security_group_ids = ["sg-12345678"]
iam_instance_profile = "github-actions-runner-instance"
metrics_port = 9090

[github_app]
client_id = "Iv1.xxx"
installation_id = 123456
private_key = """-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----"""
```

### Flag overrides

Only operational flags are accepted on the command line; everything else comes from the TOML file. Flags override the config file when set.

| Flag | Applies to | Description |
|------|------------|-------------|
| `--config` | `run`, `delete` | Path to TOML config file (required for `run`) |
| `--log-level` | `run`, `delete` | Overrides `log_level` |
| `--log-format` | `run`, `delete` | Overrides `log_format` |
| `--version` | `run` | Print version information |

For `delete`, all flags from the legacy CLI (`--url`, `--name`, `--app-client-id`, etc.) are still accepted and override the config file when explicitly set, so ad-hoc deletes without a config file remain supported.

### Prometheus Metrics

The scaler can optionally expose Prometheus metrics for monitoring:

```bash
# config.toml
# ...
# metrics_port = 9090

./scaleset-ec2-provider run --config config.toml
```

When `metrics_port` is set (default: 0/disabled), an HTTP endpoint is available at `http://localhost:9090/metrics`.

#### Available Metrics

**Runner Metrics:**
- `scaleset_ec2_provider_runners_idle` - Number of idle runners
- `scaleset_ec2_provider_runners_busy` - Number of busy runners
- `scaleset_ec2_provider_runners_total` - Total number of runners
- `scaleset_ec2_provider_desired_runners` - Desired number of runners as reported by the listener

**Job Metrics:**
- `scaleset_ec2_provider_jobs_started_total` - Total number of jobs started
- `scaleset_ec2_provider_jobs_completed_total` - Total number of jobs completed
- `scaleset_ec2_provider_job_duration_seconds` - Histogram of job execution duration

**EC2 Instance Metrics:**
- `scaleset_ec2_provider_instances_created_total` - Total number of EC2 instances created
- `scaleset_ec2_provider_instances_terminated_total` - Total number of EC2 instances terminated
- `scaleset_ec2_provider_instance_lifetime_seconds` - Histogram of instance lifetime from creation to termination
- `scaleset_ec2_provider_instance_startup_seconds` - Histogram of instance startup time

**GitHub Statistics:**
- `scaleset_ec2_provider_github_available_jobs` - Total available jobs reported by GitHub
- `scaleset_ec2_provider_github_acquired_jobs` - Total acquired jobs reported by GitHub
- `scaleset_ec2_provider_github_assigned_jobs` - Total assigned jobs reported by GitHub
- `scaleset_ec2_provider_github_running_jobs` - Total running jobs reported by GitHub
- `scaleset_ec2_provider_github_registered_runners` - Total registered runners reported by GitHub
- `scaleset_ec2_provider_github_busy_runners` - Total busy runners reported by GitHub
- `scaleset_ec2_provider_github_idle_runners` - Total idle runners reported by GitHub

**Error Metrics:**
- `scaleset_ec2_provider_errors_total` - Total number of errors by type (labels: `error_type`)

**Info Metrics:**
- `scaleset_ec2_provider_info` - Scaler information with labels (scale_set_name, instance_types, region, ami_id)

#### Labels

All metrics include the following labels for identification and filtering:
- `scale_set_name` - Name of the scale set
- `instance_types` - EC2 instance type(s) (e.g., "t3.medium")
- `region` - AWS region
- `ami_id` - AMI ID used for instances

Error metrics have an additional label:
- `error_type` - Type of error (e.g., "ec2_run", "ec2_run_capacity", "ec2_terminate", "jit_config")

#### Example Queries

```promql
# Total active runners
scaleset_ec2_provider_runners_total

# Desired vs actual runner count
scaleset_ec2_provider_desired_runners - scaleset_ec2_provider_runners_total

# Runner utilization percentage
scaleset_ec2_provider_github_busy_runners / scaleset_ec2_provider_github_registered_runners * 100

# Pending (unassigned) jobs
scaleset_ec2_provider_github_available_jobs - scaleset_ec2_provider_github_assigned_jobs

# Job completion rate over 5 minutes
rate(scaleset_ec2_provider_jobs_completed_total[5m])

# 95th percentile job duration
histogram_quantile(0.95, rate(scaleset_ec2_provider_job_duration_seconds_bucket[5m]))

# 95th percentile instance startup time
histogram_quantile(0.95, rate(scaleset_ec2_provider_instance_startup_seconds_bucket[5m]))

# Error rate by type
sum by (error_type) (rate(scaleset_ec2_provider_errors_total[5m]))

# Instance creation vs termination
rate(scaleset_ec2_provider_instances_created_total[5m]) - rate(scaleset_ec2_provider_instances_terminated_total[5m])
```

