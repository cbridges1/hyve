{{/*
hyve.controller.watchHomeOrganizations: non-empty when hyve-controller should
also reconcile every organization still on the home cluster — a
multi-tenant install whose datastore (Postgres) the controller can share
with hyve-api. See cmd/controller's --watch-home-organizations.
*/}}
{{- define "hyve.controller.watchHomeOrganizations" -}}
{{- if and .Values.api.multiTenant.enabled (eq .Values.api.db.driver "postgres") -}}true{{- end -}}
{{- end -}}
