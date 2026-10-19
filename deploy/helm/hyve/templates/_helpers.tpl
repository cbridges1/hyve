{{/* The image every hyve component runs. */}}
{{- define "hyve.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end -}}
