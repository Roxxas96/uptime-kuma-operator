{{/*
Chart name, truncated for use in resource names.
*/}}
{{- define "uptime-kuma-operator.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name, respecting .Release.Name.
*/}}
{{- define "uptime-kuma-operator.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Chart name and version, used in the helm.sh/chart label.
*/}}
{{- define "uptime-kuma-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels applied to every resource in the chart.
*/}}
{{- define "uptime-kuma-operator.labels" -}}
helm.sh/chart: {{ include "uptime-kuma-operator.chart" . }}
{{ include "uptime-kuma-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels — kept minimal and stable, never changed across upgrades
since selectors are immutable on Deployments.
*/}}
{{- define "uptime-kuma-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "uptime-kuma-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Name of the ServiceAccount to use, honoring serviceAccount.create/name.
*/}}
{{- define "uptime-kuma-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "uptime-kuma-operator.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
