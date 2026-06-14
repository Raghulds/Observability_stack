# Observability Stack

A hands-on walkthrough of running a metrics + logs + traces observability stack on a local **kind** cluster. A single Go HTTP server in [`app/`](app) (`obs`) acts as the workload: it exposes Prometheus metrics on `/metrics`, emits structured JSON logs on stdout, and exports OpenTelemetry traces over OTLP. Three operator/agent-driven stacks observe it independently — the three pillars of observability:

- **Metrics**: kube-prometheus-stack (Prometheus Operator + Prometheus + Grafana + Alertmanager + node-exporter + kube-state-metrics) scrapes `/metrics` via a `ServiceMonitor`.
- **Logs**: ECK (Elastic Cloud on Kubernetes) deploys Elasticsearch + Kibana. A Fluent Bit DaemonSet tails the obs container's log file and ships records to Elasticsearch.
- **Traces**: the obs app is instrumented with the OpenTelemetry Go SDK and exports spans via OTLP to an OpenTelemetry Collector, which forwards them to Jaeger for storage and visualization.

```text
                            ┌──────────────────────────────┐
                            │ obs Pod                      │
                            │   /metrics   :2112  ─────────┼──> Prometheus    ──> Grafana
                            │   stdout (slog JSON) ────────┼──> Fluent Bit    ──> Elasticsearch ──> Kibana
                            │   OTLP spans ────────────────┼──> OTel Collector ──> Jaeger
                            └──────────────────────────────┘
```

## Guides

Follow them in this order on a fresh cluster:

1. [`METRICS.md`](METRICS.md) — create the kind cluster, install kube-prometheus-stack, build and deploy the obs Go app, watch Prometheus auto-discover it via the `ServiceMonitor`, run PromQL queries.
2. [`LOGS.md`](LOGS.md) — install the ECK operator, deploy Elasticsearch and Kibana via CRDs, install Fluent Bit as a DaemonSet scoped to the obs container, verify the pipeline, run KQL queries in Kibana. Requires the obs Deployment from the metrics guide.
3. [`TRACES.md`](TRACES.md) — deploy Jaeger and an OpenTelemetry Collector, rebuild the OTel-instrumented obs app, drive load via `/simulate`, and explore the request waterfall in the Jaeger UI. Requires the obs Deployment from the metrics guide.

## Repo layout

```text
app/                              Go HTTP server with Prometheus instrumentation + slog JSON logging
  main.go
  Dockerfile
  go.mod / go.sum
k8s/
  deployment.yaml                 obs Deployment in `monitoring` namespace
  service.yaml                    obs Service (named port: metrics)
  servicemonitor.yaml             ServiceMonitor (label release=monitoring) for Prometheus discovery
  logging/
    namespace.yaml                logging namespace
    elasticsearch.yaml            Elasticsearch CR (ECK)
    kibana.yaml                   Kibana CR (ECK)
    fluentbit-values.yaml         Helm values for fluent/fluent-bit
  tracing/
    namespace.yaml                tracing namespace
    jaeger.yaml                   Jaeger all-in-one Deployment + Service (OTLP in, UI 16686)
    otel-collector.yaml           OpenTelemetry Collector ConfigMap + Deployment + Service
custom_kube_prometheus_stack.yml  Local overrides for the kube-prometheus-stack chart
METRICS.md                        Metrics guide
LOGS.md                           Logs guide
TRACES.md                         Traces guide
```

## Prerequisites

- macOS or Linux with Docker Desktop or Rancher Desktop running
- [`kind`](https://kind.sigs.k8s.io/), [`kubectl`](https://kubernetes.io/docs/tasks/tools/), and [`helm`](https://helm.sh/) on `PATH`
- Roughly 6 GiB free memory for the kind node (Elasticsearch alone uses ~2 GiB)
