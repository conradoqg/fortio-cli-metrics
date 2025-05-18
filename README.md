 # fortio-cli-metrics

 fortio-cli-metrics is a command-line tool that runs [Fortio](https://fortio.org) load tests defined in a YAML configuration file and exposes the results as Prometheus metrics.

 ## Features
 - Run multiple HTTP load tests concurrently using Fortio
 - Expose latency percentiles, QPS, success/failure counts, and run counts as Prometheus metrics
 - Export test configuration values (QPS, concurrency, duration, jitter, uniform) as metrics
 - CORS support for metrics scraping from browsers

 ## Prerequisites
 - Go 1.23 or later

 ## Installation
 
 ### From source
 ```bash
 git clone https://github.com/<your-org>/fortio-cli-metrics.git
 cd fortio-cli-metrics
 go install ./cmd/fortio-cli-metrics
 ```

 This installs the `fortio-cli-metrics` binary into your `$GOBIN` or `$GOPATH/bin`.

 ### Using `go install`
 ```bash
 go install github.com/<your-org>/fortio-cli-metrics/cmd/fortio-cli-metrics@latest
 ```

 ## Usage

 ```bash
 fortio-cli-metrics [flags]
 ```

 Available flags:
 - `--metrics-addr string`  Address for Prometheus metrics endpoint (default ":9090")
 - `--config string`        Path to YAML config file defining tests (default "config.yaml")
 - `--cors-origins string`  Comma-separated list of allowed CORS origins (default "*")

 Example:
 ```bash
 fortio-cli-metrics --metrics-addr ":8080" --config config.sample.yaml
 ```

 Then configure Prometheus to scrape metrics from `http://<host>:8080/metrics`.

 ## Configuration

 fortio-cli-metrics uses a YAML file (default `config.yaml`) to define one or more load tests. See `config.sample.yaml` for an example:

 ```yaml
 # Global default duration for all tests (e.g., "60s"). If omitted, defaults to 5s per run.
 duration: 60s

 tests:
   - name: example_http
     url: https://www.example.com
     qps: 10
     concurrency: 2
     duration: 30s           # per-test override (optional)
     headers:
       User-Agent: fortio-cli-metrics/1.0
       Accept: application/json
     jitter: true            # enable +/-10% jitter
     uniform: false          # disable uniform staggering
 ```

 Field descriptions:
 - `duration` (global): Default duration for each run if not set per-test
 - `tests`: List of test configurations
   - `name`: Unique name of the test
   - `url`: Request URL
   - `qps`: Queries per second
   - `concurrency`: Number of concurrent threads
   - `duration`: (optional) Override duration per test
   - `headers`: (optional) Map of HTTP headers
   - `jitter`: (optional) Enable QPS jitter
   - `uniform`: (optional) Enable uniform staggering

 ## Prometheus Metrics

 Example metrics exposed (labelled by `test`):
 - `fortio_request_duration_seconds_avg`
 - `fortio_request_duration_seconds_p50`
 - `fortio_request_duration_seconds_p90`
 - `fortio_request_duration_seconds_p99`
 - `fortio_actual_qps`
 - `fortio_requests_success_total`
 - `fortio_requests_failure_total`
 - `fortio_test_runs_total`
 - `fortio_test_config_qps`
 - `fortio_test_config_concurrency`
 - `fortio_test_config_duration_seconds`
 - `fortio_test_config_jitter`
 - `fortio_test_config_uniform`

 ## Contributing

 Contributions are welcome! Please open an issue or submit a pull request.