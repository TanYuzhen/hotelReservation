{{- define "hotelreservation.templates.baseService" }}
apiVersion: v1
kind: Service
metadata:
  name: {{ .Values.name }}-{{ include "hotel-reservation.fullname" . }}
spec:
  type: {{ .Values.serviceType | default .Values.global.serviceType }}
  ports:
  {{- range .Values.ports }}
  - name: {{ if or (eq $.Values.name "geo") (eq $.Values.name "profile") (eq $.Values.name "recommendation") (eq $.Values.name "user") (eq $.Values.name "search") (eq $.Values.name "rate") (eq $.Values.name "review") (eq $.Values.name "reservation") }}grpc{{ else }}{{ .port | quote }}{{ end }}
    port: {{ .port }}
    {{- if .protocol}}
    protocol: {{ .protocol }}
    {{- end }}
    targetPort: {{ .targetPort }}
  {{- end }}
  selector:
    {{- include "hotel-reservation.selectorLabels" . | nindent 4 }}
    service: {{ .Values.name }}-{{ include "hotel-reservation.fullname" . }}
{{- end }}