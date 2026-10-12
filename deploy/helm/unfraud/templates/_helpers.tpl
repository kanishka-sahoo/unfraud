{{- define "unfraud.labels" -}}
app.kubernetes.io/part-of: unfraud
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "unfraud.bootstrap" -}}
{{ .Values.kafka.name }}-kafka-bootstrap:9092
{{- end -}}
