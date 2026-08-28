{{/* Chart name, overridable. */}}
{{- define "arex.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified release name. */}}
{{- define "arex.fullname" -}}
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

{{- define "arex.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "arex.labels" -}}
helm.sh/chart: {{ include "arex.chart" . }}
{{ include "arex.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "arex.selectorLabels" -}}
app.kubernetes.io/name: {{ include "arex.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "arex.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "arex.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
The rendered arex config.

passwordFile is set by the chart rather than by the operator when a Secret is
named: the path is decided here, by the volumeMount, so letting it be typed
separately in values would only create a way for the two to disagree.
*/}}
{{- define "arex.config" -}}
{{- $config := deepCopy .Values.config -}}
{{- if .Values.credentials.existingSecret -}}
{{- $path := printf "%s/%s" (trimSuffix "/" .Values.credentials.mountPath) .Values.credentials.key -}}
{{- $_ := set $config "passwordFile" $path -}}
{{- end -}}
{{- with .Values.listen.tls -}}
{{- if .existingSecret -}}
{{- $dir := trimSuffix "/" .mountPath -}}
{{- $tls := dict "certFile" (printf "%s/%s" $dir .certKey) "keyFile" (printf "%s/%s" $dir .keyKey) -}}
{{- if .clientCAKey -}}
{{- $_ := set $tls "clientCAFile" (printf "%s/%s" $dir .clientCAKey) -}}
{{- end -}}
{{- $_ := set $config "listenTLS" $tls -}}
{{- end -}}
{{- end -}}
{{- if gt (int .Values.listen.probePort) 0 -}}
{{- $_ := set $config "probeAddress" (printf ":%d" (int .Values.listen.probePort)) -}}
{{- end -}}
{{- with .Values.listen.basicAuth -}}
{{- if .existingSecret -}}
{{- $basic := dict "username" .username "passwordFile" (printf "%s/%s" (trimSuffix "/" .mountPath) .key) -}}
{{- $_ := set $config "listenAuth" (dict "basic" $basic) -}}
{{- end -}}
{{- end -}}
{{- toYaml $config -}}
{{- end -}}

{{/* Whether the metrics endpoint is served over TLS, which the ServiceMonitor
needs to know. */}}
{{- define "arex.scheme" -}}
{{- if .Values.listen.tls.existingSecret -}}HTTPS{{- else -}}HTTP{{- end -}}
{{- end -}}

{{/*
The scheme and port a probe should use.

With a probe listener the probes go there, in plain HTTP, whatever the metrics
endpoint is doing. Without one they share the metrics listener and follow its
scheme.
*/}}
{{- define "arex.probeScheme" -}}
{{- if gt (int .Values.listen.probePort) 0 -}}HTTP{{- else -}}{{ include "arex.scheme" . }}{{- end -}}
{{- end -}}

{{- define "arex.probePort" -}}
{{- if gt (int .Values.listen.probePort) 0 -}}probes{{- else -}}metrics{{- end -}}
{{- end -}}

{{/*
A probe, with the scheme filled in when it is an httpGet.

Kubernetes rejects a probe carrying two handlers, and Helm merges maps rather
than replacing them -- so overriding the default httpGet with a tcpSocket
requires "httpGet: null" in values, which arrives here as a nil rather than an
absent key. Both cases are handled: only a real httpGet map gets a scheme.
*/}}
{{- define "arex.probe" -}}
{{- $p := deepCopy .probe -}}
{{- if kindIs "map" (get $p "httpGet") -}}
{{- $_ := set (get $p "httpGet") "scheme" (include "arex.probeScheme" .root) -}}
{{- $_ := set (get $p "httpGet") "port" (include "arex.probePort" .root) -}}
{{- else -}}
{{- $_ := unset $p "httpGet" -}}
{{- end -}}
{{- toYaml $p -}}
{{- end -}}
