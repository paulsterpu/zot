package extensions

import "time"

type ExtensionConfig struct {
	Search  *SearchConfig
	Metrics *MetricsConfig
}

type SearchConfig struct {
	// CVE search
	CVE    *CVEConfig
	Enable bool
}

type CVEConfig struct {
	UpdateInterval time.Duration // should be 2 hours or more, if not specified default be kept as 24 hours
}

type MetricsConfig struct {
	Enable     bool
	Prometheus *PrometheusConfig
}

type PrometheusConfig struct {
	Path string // default is "/metrics"
}
