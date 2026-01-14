APPS_CHART_DIR = 'deploy/helm/apps'
INFRA_CHART_DIR = 'deploy/helm/infra'
SERVICES = ['ingest']

k8s_yaml(helm(
  APPS_CHART_DIR,
  name='lea',
  values=['%s/values.yaml' % APPS_CHART_DIR],
))

k8s_yaml(helm(
  INFRA_CHART_DIR,
  name='lea-infra',
  values=['%s/values.yaml' % INFRA_CHART_DIR],
))

docker_build('ingest-svc', '.', dockerfile='services/ingest-svc/Dockerfile')

for svc in SERVICES:
  k8s_resource('lea-%s' % svc, labels=['services'])
