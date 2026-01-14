{{- define "llm-event-analysis-apps.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "llm-event-analysis-apps.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "llm-event-analysis-apps.labels" -}}
app.kubernetes.io/name: {{ include "llm-event-analysis-apps.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "llm-event-analysis-apps.selectorLabels" -}}
app.kubernetes.io/name: {{ include "llm-event-analysis-apps.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "llm-event-analysis-apps.formatterURL" -}}
{{- $svc := printf "%s-formatter" (include "llm-event-analysis-apps.fullname" .) -}}
{{- $port := int .Values.formatter.service.port -}}
{{- if eq $port 80 -}}
{{- printf "http://%s/format" $svc -}}
{{- else -}}
{{- printf "http://%s:%d/format" $svc $port -}}
{{- end -}}
{{- end -}}
