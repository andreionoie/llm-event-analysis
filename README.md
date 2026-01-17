# Real-time LLM event analysis & triaging platform

![System Diagram](./system_diagram.excalidraw.svg)

## Tech stack
Go • Kafka • PostgreSQL • Redis • Gemini API • Kubernetes/Helm • Prometheus

## Quickstart
Prerequisites: Docker, kubectl, Tilt, Helm, local k8s cluster (e.g. orbstack, kind, minikube)
```bash
# Pull Helm dependencies for shared services (Kafka, Postgres, Redis, Prometheus)
helm dependency update deploy/helm/infra

# Start the platform
tilt up

# Ingest events
curl http://lea-ingest.default.svc.cluster.local/events \
  -H "Content-Type: application/json" \
  --data '{"source": "firewall", "type": "connection_blocked", "severity": "info", "payload": {"ip": "192.168.127.12"}}'

# Run triage analysis
curl http://lea-analyzer.default.svc.cluster.local/triage/jobs \
  -H "Content-Type: application/json" \
  --data '{"time_range": {"start": "2026-01-01T00:00:00Z", "end": "2026-01-02T00:00:00Z"}}'
```