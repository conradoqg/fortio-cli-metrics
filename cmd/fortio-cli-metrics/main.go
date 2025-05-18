package main

import (
    "flag"
    "fmt"
    "log"
    "net/http"
    "io"
    "os"
    "os/signal"
    "strings"
    "strconv"
    "time"

    "gopkg.in/yaml.v3"

    "fortio.org/fortio/fhttp"
    "fortio.org/fortio/periodic"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    // Fortio log for controlling verbosity
    flog "fortio.org/log"
)

// TestConfig defines a Fortio load test configuration.
// TestConfig defines a Fortio load test configuration.
type TestConfig struct {
    Name        string            `yaml:"name"`        // Unique name of the test
    URL         string            `yaml:"url"`         // Request URL
    QPS         float64           `yaml:"qps"`         // Queries per second
    Concurrency int               `yaml:"concurrency"` // Number of concurrent threads
    Duration    string            `yaml:"duration,omitempty"` // Optional duration override (e.g. "30s")
    Headers     map[string]string `yaml:"headers,omitempty"`  // Optional HTTP headers
    Jitter      bool              `yaml:"jitter,omitempty"`   // Enable QPS jitter (+/-10%)
    Uniform     bool              `yaml:"uniform,omitempty"`  // Enable uniform staggers between threads
}

// Tests will be loaded from a YAML config file (see Config).
var tests []TestConfig

// Config is the YAML structure for the CLI configuration.
type Config struct {
    GlobalDuration string       `yaml:"duration,omitempty"` // Global default duration (e.g. "60s")
    // LogLevel sets verbosity for application and Fortio logs (Debug, Verbose, Info, Warning, Error, Critical)
    LogLevel       string       `yaml:"log_level,omitempty"`
    Tests          []TestConfig `yaml:"tests"`
}
// corsMiddleware wraps an HTTP handler and sets CORS headers.
func corsMiddleware(h http.Handler, allowed []string) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        if len(allowed) == 1 && allowed[0] == "*" {
            w.Header().Set("Access-Control-Allow-Origin", "*")
        } else {
            for _, ao := range allowed {
                if ao == origin {
                    w.Header().Set("Access-Control-Allow-Origin", origin)
                    break
                }
            }
        }
        w.Header().Set("Vary", "Origin")
        if r.Method == http.MethodOptions {
            w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
            w.Header().Set("Access-Control-Allow-Headers", "Accept, Accept-Encoding, Authorization, Content-Type")
            w.WriteHeader(http.StatusNoContent)
            return
        }
        h.ServeHTTP(w, r)
    })
}

func main() {
   metricsAddr := flag.String("metrics-addr", ":9090", "Address for Prometheus metrics endpoint")
   metricsPath := flag.String("metrics-path", "/metrics", "HTTP path for Prometheus metrics endpoint (default '/metrics')")
   configPath := flag.String("config", "config.yaml", "Path to YAML config file defining tests")
   corsOrigins := flag.String("cors-origins", "*", "Comma-separated list of allowed CORS origins (default '*')")
    flag.Parse()

    // Handle Ctrl+C to exit process immediately
    stopCh := make(chan os.Signal, 1)
    signal.Notify(stopCh, os.Interrupt)
    go func() {
        <-stopCh
        log.Println("Received interrupt, shutting down")
        os.Exit(0)
    }()
    // Load and parse tests from YAML config
    data, err := os.ReadFile(*configPath)
    if err != nil {
        log.Fatalf("failed to read config file %s: %v", *configPath, err)
    }
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        log.Fatalf("failed to parse config file: %v", err)
    }
    // Apply log level from config if set
    if cfg.LogLevel != "" {
        if err := flog.SetLogLevelStr(cfg.LogLevel); err != nil {
            log.Fatalf("invalid log level '%s': %v", cfg.LogLevel, err)
        }
    }
    if len(cfg.Tests) == 0 {
        log.Fatalf("no tests defined in config file %s", *configPath)
    }
    // Parse global duration if provided
    var globalDur time.Duration
    if cfg.GlobalDuration != "" {
        globalDur, err = time.ParseDuration(cfg.GlobalDuration)
        if err != nil {
            log.Fatalf("invalid global duration '%s': %v", cfg.GlobalDuration, err)
        }
    }
    // Populate tests from configuration
    tests = cfg.Tests

    // Create a Prometheus registry
    registry := prometheus.NewRegistry()

    // Define Prometheus metrics
    latencyAvg := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_request_duration_seconds_avg",
            Help: "Average request latency in seconds (excludes connection setup time)",
        }, []string{"test_name"},
    )
    latencyP50 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_request_duration_seconds_p50",
            Help: "50th percentile request latency in seconds (excludes connection setup time)",
        }, []string{"test_name"},
    )
    latencyP90 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_request_duration_seconds_p90",
            Help: "90th percentile request latency in seconds (excludes connection setup time)",
        }, []string{"test_name"},
    )
    latencyP99 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_request_duration_seconds_p99",
            Help: "99th percentile request latency in seconds (excludes connection setup time)",
        }, []string{"test_name"},
    )
    actualQPS := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_actual_qps",
            Help: "Actual queries per second observed",
        }, []string{"test_name"},
    )
   // Connection time metrics
    connAvg := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_connection_duration_seconds_avg",
            Help: "Average connection setup time in seconds (TCP + TLS handshake)",
        }, []string{"test_name"},
    )
    connP50 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_connection_duration_seconds_p50",
            Help: "50th percentile connection setup time in seconds (TCP + TLS handshake)",
        }, []string{"test_name"},
    )
    connP90 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_connection_duration_seconds_p90",
            Help: "90th percentile connection setup time in seconds (TCP + TLS handshake)",
        }, []string{"test_name"},
    )
    connP99 := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_connection_duration_seconds_p99",
            Help: "99th percentile connection setup time in seconds (TCP + TLS handshake)",
        }, []string{"test_name"},
    )
    // Success and failure counts per test run (gauges)
    successCount := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_requests_success",
            Help: "Number of successful requests in the last test run",
        }, []string{"test_name"},
    )
    failureCount := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_requests_failure",
            Help: "Number of failed requests in the last test run",
        }, []string{"test_name"},
    )

    // Counter for total runs per test
    runCount := prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "fortio_test_runs_total",
            Help: "Total number of Fortio test runs executed",
        }, []string{"test_name"},
    )
    // Config metrics: reflect configured QPS, concurrency, duration, jitter, uniform
    configQPS := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_test_config_qps",
            Help: "Configured QPS for the Fortio test",
        }, []string{"test_name"},
    )
    configConcurrency := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_test_config_concurrency",
            Help: "Configured concurrency for the Fortio test",
        }, []string{"test_name"},
    )
    configDuration := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_test_config_duration_seconds",
            Help: "Configured duration (seconds) for the Fortio test",
        }, []string{"test_name"},
    )
    configJitter := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_test_config_jitter",
            Help: "Whether jitter is enabled for the Fortio test (1 = true)",
        }, []string{"test_name"},
    )
    configUniform := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_test_config_uniform",
            Help: "Whether uniform staggering is enabled for the Fortio test (1 = true)",
        }, []string{"test_name"},
    )
    // HTTP status codes count per test run
    httpCodeCount := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "fortio_http_response_code_count",
            Help: "Number of HTTP responses per status code in the last test run",
        }, []string{"test_name", "status_code"},
    )
    // Register metrics
    registry.MustRegister(
        latencyAvg, latencyP50, latencyP90, latencyP99,
        actualQPS,
        connAvg, connP50, connP90, connP99,
        successCount, failureCount,
        runCount,
        configQPS, configConcurrency, configDuration,
        configJitter, configUniform,
        httpCodeCount,
    )

    // Expose test configuration as metrics
    for _, tc := range tests {
        configQPS.WithLabelValues(tc.Name).Set(tc.QPS)
        configConcurrency.WithLabelValues(tc.Name).Set(float64(tc.Concurrency))
        // Determine duration in seconds
        dsec := globalDur.Seconds()
        if tc.Duration != "" {
            if d, err := time.ParseDuration(tc.Duration); err == nil {
                dsec = d.Seconds()
            }
        }
        configDuration.WithLabelValues(tc.Name).Set(dsec)
        // Jitter and uniform flags
        if tc.Jitter {
            configJitter.WithLabelValues(tc.Name).Set(1)
        } else {
            configJitter.WithLabelValues(tc.Name).Set(0)
        }
        if tc.Uniform {
            configUniform.WithLabelValues(tc.Name).Set(1)
        } else {
            configUniform.WithLabelValues(tc.Name).Set(0)
        }
    }
    // Perform a sample request per test to log the target URL and check connectivity
    for _, tc := range tests {
        log.Printf("Performing sample request for test %s to %s", tc.Name, tc.URL)
        req, err := http.NewRequest(http.MethodGet, tc.URL, nil)
        if err != nil {
            log.Printf("error creating sample request for test %s: %v", tc.Name, err)
            continue
        }
        for hn, hv := range tc.Headers {
            // Honor Host header override by setting Request.Host, others via Header
            if strings.ToLower(hn) == "host" {
                req.Host = hv
            } else {
                req.Header.Set(hn, hv)
            }
        }
        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            log.Printf("error performing sample request for test %s: %v", tc.Name, err)
            continue
        }
        log.Printf("sample response for test %s: status %s", tc.Name, resp.Status)
        // Print response headers
        for header, values := range resp.Header {
            log.Printf("sample response for test %s: header %s: %v", tc.Name, header, values)
        }
        // Print response body
        bodyData, err := io.ReadAll(resp.Body)
        if err != nil {
            log.Printf("sample response for test %s: error reading body: %v", tc.Name, err)
        } else {
            log.Printf("sample response for test %s: body: %s", tc.Name, string(bodyData))
        }
        resp.Body.Close()
    }
    // Start each test in its own goroutine
    for _, tc := range tests {
        go runTest(
            tc, globalDur,
            latencyAvg, latencyP50, latencyP90, latencyP99,
            connAvg, connP50, connP90, connP99,
            actualQPS, successCount, failureCount,
            runCount,
            httpCodeCount,
        )
    }

    // Prepare CORS
    allowed := strings.Split(*corsOrigins, ",")
    // HTTP handler for metrics with CORS
    h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
   http.Handle(*metricsPath, corsMiddleware(h, allowed))
   log.Printf("Starting metrics server at %s%s", *metricsAddr, *metricsPath)
    log.Fatal(http.ListenAndServe(*metricsAddr, nil))
}

// runTest continuously runs the Fortio HTTP test for the configured duration and exports Prometheus metrics.
// runTest continuously executes a Fortio HTTP test and exports Prometheus metrics.
func runTest(
    tc TestConfig,
    globalDur time.Duration,
    latencyAvg, latencyP50, latencyP90, latencyP99 *prometheus.GaugeVec,
    connAvg, connP50, connP90, connP99 *prometheus.GaugeVec,
    actualQPS *prometheus.GaugeVec,
    successCount, failureCount *prometheus.GaugeVec,
    runCount *prometheus.CounterVec,
    httpCodeCount *prometheus.GaugeVec,
) {
    // Determine per-test duration (override global if set)
    dur := globalDur
    if tc.Duration != "" {
        if d, err := time.ParseDuration(tc.Duration); err != nil {
            log.Printf("invalid duration for test %s: %v, using global %v", tc.Name, err, globalDur)
        } else {
            dur = d
        }
    }
    // Prepare HTTP options
    httpOpts := fhttp.NewHTTPOptions(tc.URL)
    runnerOpts := &fhttp.HTTPRunnerOptions{
        RunnerOptions: periodic.RunnerOptions{
            QPS:         tc.QPS,
            NumThreads:  tc.Concurrency,
            Duration:    dur,
            Percentiles: []float64{50.0, 90.0, 99.0},
            Jitter:      tc.Jitter,
            Uniform:     tc.Uniform,
        },
        HTTPOptions: *httpOpts,
    }
    // Disable Fortio's internal signal handlers to allow immediate Ctrl+C kill
    runnerOpts.Stop = periodic.NewAborter()
    // Apply any custom HTTP headers
    for hn, hv := range tc.Headers {
        hdr := fmt.Sprintf("%s: %s", hn, hv)
        if err := runnerOpts.AddAndValidateExtraHeader(hdr); err != nil {
            log.Printf("warning: invalid header %q for test %s: %v", hdr, tc.Name, err)
        }
    }
    for {
        // Count this run
        runCount.WithLabelValues(tc.Name).Inc()
        // Execute the HTTP test
        res, err := fhttp.RunHTTPTest(runnerOpts)
        if err != nil {
            log.Printf("error running test %s: %v", tc.Name, err)
            failureCount.WithLabelValues(tc.Name).Inc()
            continue
        }
        // Record latency metrics
        hist := res.DurationHistogram
        latencyAvg.WithLabelValues(tc.Name).Set(hist.Avg)
        // Map percentiles
        pMap := make(map[float64]float64, len(hist.Percentiles))
        for _, p := range hist.Percentiles {
            pMap[p.Percentile] = p.Value
        }
        if v, ok := pMap[50.0]; ok {
            latencyP50.WithLabelValues(tc.Name).Set(v)
        }
        if v, ok := pMap[90.0]; ok {
            latencyP90.WithLabelValues(tc.Name).Set(v)
        }
        if v, ok := pMap[99.0]; ok {
            latencyP99.WithLabelValues(tc.Name).Set(v)
        }
        // Record actual QPS
        actualQPS.WithLabelValues(tc.Name).Set(res.ActualQPS)
        // Record success and failure counts for this run (not cumulative)
        successCount.WithLabelValues(tc.Name).Set(float64(hist.Count))
        failureCount.WithLabelValues(tc.Name).Set(float64(res.ErrorsDurationHistogram.Count))
        // Record HTTP status code counts for this run
        for code, count := range res.RetCodes {
            httpCodeCount.WithLabelValues(tc.Name, strconv.Itoa(code)).Set(float64(count))
        }
        // Record connection time metrics for this run
        if cs := res.ConnectionStats; cs != nil {
            connAvg.WithLabelValues(tc.Name).Set(cs.Avg)
            cMap := make(map[float64]float64, len(cs.Percentiles))
            for _, p := range cs.Percentiles {
                cMap[p.Percentile] = p.Value
            }
            if v, ok := cMap[50.0]; ok {
                connP50.WithLabelValues(tc.Name).Set(v)
            }
            if v, ok := cMap[90.0]; ok {
                connP90.WithLabelValues(tc.Name).Set(v)
            }
            if v, ok := cMap[99.0]; ok {
                connP99.WithLabelValues(tc.Name).Set(v)
            }
        }
    }
}