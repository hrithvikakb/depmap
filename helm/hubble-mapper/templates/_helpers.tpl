{{/*
Expand the name of the chart.
*/}}
{{- define "hubble-mapper.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "hubble-mapper.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hubble-mapper.labels" -}}
helm.sh/chart: {{ include "hubble-mapper.chart" . }}
{{ include "hubble-mapper.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hubble-mapper.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hubble-mapper.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }} 