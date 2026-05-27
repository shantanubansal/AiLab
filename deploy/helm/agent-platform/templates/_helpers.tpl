{{/*
Common labels and naming helpers.
*/}}

{{- define "agent-platform.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "agent-platform.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "agent-platform.labels" -}}
app.kubernetes.io/name: {{ include "agent-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
ailab.uipath.com/profile: {{ .Values.profile | quote }}
{{- end -}}

{{- define "agent-platform.image" -}}
{{ .Values.image.registry }}/{{ .component }}:{{ .Values.image.tag }}
{{- end -}}
