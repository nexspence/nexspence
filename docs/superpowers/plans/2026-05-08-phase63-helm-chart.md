# Phase 63: Helm Chart for Nexspence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package Nexspence as a production-ready Helm chart with pluggable ingress (nginx, traefik, cilium) and API gateway (istio, cilium Gateway API) support.

**Architecture:** Single chart under `deploy/helm/nexspence/` with a `values.yaml` that has three networking sections — `ingress` (standard K8s Ingress for nginx/traefik/cilium ingress-controller), `gateway.istio` (Istio `Gateway` + `VirtualService`), and `gateway.cilium` (K8s Gateway API `Gateway` + `HTTPRoute`). Exactly one networking mode is active at a time; the rest are templated behind `enabled: false`. An optional bitnami/postgresql sub-chart handles in-cluster Postgres; for production, users disable it and supply an external DSN.

**Tech Stack:** Helm 3.x, Kubernetes ≥ 1.26, optional Istio ≥ 1.17, optional Cilium ≥ 1.14 (Gateway API CRDs required), bitnami/postgresql 15.x sub-chart.

---

## File Map

| File | Purpose |
|------|---------|
| `deploy/helm/nexspence/Chart.yaml` | Chart metadata, bitnami/postgresql dependency |
| `deploy/helm/nexspence/values.yaml` | All defaults; full annotations for every flag |
| `deploy/helm/nexspence/templates/_helpers.tpl` | Name, label, and selector helpers |
| `deploy/helm/nexspence/templates/NOTES.txt` | Post-install connect instructions |
| `deploy/helm/nexspence/templates/serviceaccount.yaml` | Optional SA |
| `deploy/helm/nexspence/templates/configmap.yaml` | Non-secret env vars |
| `deploy/helm/nexspence/templates/secret.yaml` | DSN + JWT secret + admin password |
| `deploy/helm/nexspence/templates/pvc.yaml` | Blob storage PVC (local mode) |
| `deploy/helm/nexspence/templates/deployment.yaml` | Main Deployment with probes |
| `deploy/helm/nexspence/templates/service.yaml` | ClusterIP Service |
| `deploy/helm/nexspence/templates/ingress.yaml` | Standard Ingress (nginx/traefik/cilium) |
| `deploy/helm/nexspence/templates/istio-gateway.yaml` | Istio `Gateway` resource |
| `deploy/helm/nexspence/templates/istio-virtualservice.yaml` | Istio `VirtualService` |
| `deploy/helm/nexspence/templates/httproute.yaml` | K8s Gateway API `HTTPRoute` (cilium) |
| `deploy/helm/nexspence/templates/hpa.yaml` | HorizontalPodAutoscaler |
| `deploy/helm/nexspence/values-examples/nginx.yaml` | nginx ingress example |
| `deploy/helm/nexspence/values-examples/traefik.yaml` | traefik ingress example |
| `deploy/helm/nexspence/values-examples/cilium-ingress.yaml` | cilium ingress-controller example |
| `deploy/helm/nexspence/values-examples/istio-gateway.yaml` | istio API gateway example |
| `deploy/helm/nexspence/values-examples/cilium-gateway.yaml` | cilium Gateway API example |
| `deploy/helm/nexspence/README.md` | Chart README (ArtifactHub compatible) |
| `docs/deployment.md` | Add Helm section at the end |

---

## Task 1: Chart scaffolding — Chart.yaml + _helpers.tpl + NOTES.txt

**Files:**
- Create: `deploy/helm/nexspence/Chart.yaml`
- Create: `deploy/helm/nexspence/templates/_helpers.tpl`
- Create: `deploy/helm/nexspence/templates/NOTES.txt`

- [ ] **Step 1: Create deploy/helm/nexspence/ directory structure**

```bash
mkdir -p deploy/helm/nexspence/templates deploy/helm/nexspence/values-examples
```

- [ ] **Step 2: Write Chart.yaml**

`deploy/helm/nexspence/Chart.yaml`:
```yaml
apiVersion: v2
name: nexspence
description: Nexspence — open-source universal artifact repository manager
type: application
version: 0.1.0
appVersion: "latest"
home: https://github.com/nexspence-oss/nexspence
sources:
  - https://github.com/nexspence-oss/nexspence
keywords:
  - artifact-repository
  - nexus-alternative
  - maven
  - npm
  - docker
  - helm
maintainers:
  - name: nexspence-oss
dependencies:
  - name: postgresql
    version: "15.5.38"
    repository: "https://charts.bitnami.com/bitnami"
    condition: postgresql.enabled
```

- [ ] **Step 3: Write _helpers.tpl**

`deploy/helm/nexspence/templates/_helpers.tpl`:
```
{{/*
Expand the name of the chart.
*/}}
{{- define "nexspence.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "nexspence.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "nexspence.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "nexspence.labels" -}}
helm.sh/chart: {{ include "nexspence.chart" . }}
{{ include "nexspence.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "nexspence.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nexspence.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "nexspence.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "nexspence.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
PostgreSQL DSN — either external or bitnami sub-chart.
*/}}
{{- define "nexspence.databaseDSN" -}}
{{- if .Values.postgresql.enabled }}
{{- printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable"
    .Values.postgresql.auth.username
    .Values.postgresql.auth.password
    .Release.Name
    .Values.postgresql.auth.database }}
{{- else }}
{{- .Values.externalDatabase.dsn }}
{{- end }}
{{- end }}
```

- [ ] **Step 4: Write NOTES.txt**

`deploy/helm/nexspence/templates/NOTES.txt`:
```
Nexspence has been deployed!

1. Get the application URL:
{{- if .Values.ingress.enabled }}
  http{{ if .Values.ingress.tls }}s{{ end }}://{{ (index .Values.ingress.hosts 0).host }}
{{- else if .Values.gateway.istio.enabled }}
  Check the Istio Gateway external IP:
  kubectl get svc -n istio-system istio-ingressgateway
{{- else if .Values.gateway.cilium.enabled }}
  Check the cilium Gateway external IP:
  kubectl get gateway {{ include "nexspence.fullname" . }}-cilium -n {{ .Release.Namespace }}
{{- else }}
  kubectl port-forward svc/{{ include "nexspence.fullname" . }} 8081:8081
  http://localhost:8081
{{- end }}

2. Default credentials: admin / admin123 (change immediately!)

3. To upgrade:
   helm upgrade {{ .Release.Name }} nexspence/nexspence -f your-values.yaml
```

- [ ] **Step 5: Run helm lint to verify scaffolding**

```bash
helm dependency update deploy/helm/nexspence
helm lint deploy/helm/nexspence
```

Expected: `0 chart(s) linted, 0 chart(s) failed` (will warn about missing values.yaml — that's OK at this stage)

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/nexspence/Chart.yaml deploy/helm/nexspence/templates/_helpers.tpl deploy/helm/nexspence/templates/NOTES.txt
git commit -m "feat(helm): scaffold Chart.yaml, helpers, NOTES"
```

---

## Task 2: values.yaml — complete defaults

**Files:**
- Create: `deploy/helm/nexspence/values.yaml`

- [ ] **Step 1: Write values.yaml**

`deploy/helm/nexspence/values.yaml`:
```yaml
# -- Override chart name
nameOverride: ""
# -- Override fully qualified app name
fullnameOverride: ""

image:
  repository: ghcr.io/nexspence-oss/nexspence
  pullPolicy: IfNotPresent
  # -- Overrides the image tag (defaults to chart appVersion)
  tag: ""

imagePullSecrets: []

replicaCount: 1

serviceAccount:
  # -- Create a ServiceAccount
  create: true
  # -- Annotations to add to the service account
  annotations: {}
  # -- Override the name
  name: ""

podAnnotations: {}
podLabels: {}

podSecurityContext:
  fsGroup: 1000

securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  readOnlyRootFilesystem: false
  allowPrivilegeEscalation: false

# -- Nexspence application configuration
config:
  # -- External URL (used in download links, Docker subdomain connector)
  baseURL: "http://nexspence.example.com"
  logLevel: "info"
  logFormat: "json"
  httpListen: ":8081"
  # -- JWT secret — CHANGE IN PRODUCTION (min 32 chars)
  jwtSecret: "change-me-to-a-random-64-char-string"
  jwtExpiryHours: 8
  # -- Bootstrap admin credentials
  adminUsername: "admin"
  adminPassword: "admin123"
  adminEmail: "admin@example.com"
  adminFirstName: "Admin"

# -- Blob storage: "local" or "s3"
storage:
  type: local
  local:
    mountPath: "/blobs"
    # -- PVC size for blob storage
    size: "100Gi"
    storageClass: ""
  s3:
    # -- Filled when storage.type=s3
    endpoint: ""
    bucket: ""
    region: "us-east-1"
    accessKey: ""
    secretKey: ""
    useSSL: true

service:
  type: ClusterIP
  port: 80
  targetPort: 8081
  annotations: {}

# ── Ingress (nginx / traefik / cilium ingress-controller) ──────────────
# Exactly ONE of ingress or gateway should be enabled at a time.
ingress:
  enabled: false
  # -- IngressClass name: "nginx" | "traefik" | "cilium"
  className: "nginx"
  annotations: {}
    # nginx example:
    #   nginx.ingress.kubernetes.io/proxy-body-size: "10g"
    #   nginx.ingress.kubernetes.io/proxy-read-timeout: "600"
    # traefik example:
    #   traefik.ingress.kubernetes.io/router.entrypoints: websecure
    # cilium example:
    #   (no extra annotations required for basic cilium ingress)
  hosts:
    - host: nexspence.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []
  # tls:
  #   - secretName: nexspence-tls
  #     hosts:
  #       - nexspence.example.com

# ── API Gateway: Istio ─────────────────────────────────────────────────
gateway:
  istio:
    enabled: false
    # -- Name of the existing Istio IngressGateway service selector
    gatewaySelector:
      istio: ingressgateway
    # -- Port exposed on the Istio Gateway
    port: 80
    # -- TLS mode: SIMPLE | PASSTHROUGH | MUTUAL | ""
    tlsMode: ""
    # -- Secret name for TLS (required when tlsMode=SIMPLE)
    tlsSecret: ""
    hosts:
      - "nexspence.example.com"
    # -- Extra annotations on the VirtualService
    vsAnnotations: {}
    # -- Timeout for VirtualService routes
    timeout: "300s"

  # ── API Gateway: Cilium (K8s Gateway API) ─────────────────────────────
  cilium:
    enabled: false
    # -- Gateway API GatewayClass name (cilium registers "cilium")
    gatewayClassName: "cilium"
    hosts:
      - host: nexspence.example.com
        paths:
          - path: /
            pathType: PathPrefix
    tls: []
    # tls:
    #   - certificateRefs:
    #       - kind: Secret
    #         name: nexspence-tls
    #     hosts:
    #       - nexspence.example.com
    # -- Extra annotations on the Gateway resource
    gatewayAnnotations: {}

# ── Horizontal Pod Autoscaler ─────────────────────────────────────────
autoscaling:
  enabled: false
  minReplicas: 1
  maxReplicas: 5
  targetCPUUtilizationPercentage: 70
  targetMemoryUtilizationPercentage: 80

resources:
  requests:
    cpu: "250m"
    memory: "256Mi"
  limits:
    cpu: "2000m"
    memory: "1Gi"

# ── Liveness / Readiness probes ───────────────────────────────────────
livenessProbe:
  httpGet:
    path: /service/rest/v1/status/check
    port: 8081
  initialDelaySeconds: 20
  periodSeconds: 30
  timeoutSeconds: 5
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /service/rest/v1/status/check
    port: 8081
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 3

nodeSelector: {}
tolerations: []
affinity: {}

# ── PostgreSQL sub-chart (bitnami) ────────────────────────────────────
# Set enabled: false and provide externalDatabase.dsn for external Postgres.
postgresql:
  enabled: true
  auth:
    username: nexspence
    password: nexspence
    database: nexspence
  primary:
    persistence:
      enabled: true
      size: 10Gi

# -- External PostgreSQL DSN (used when postgresql.enabled=false)
externalDatabase:
  dsn: "postgres://nexspence:nexspence@external-postgres:5432/nexspence?sslmode=disable"
```

- [ ] **Step 2: Verify helm lint passes**

```bash
helm lint deploy/helm/nexspence
```

Expected: `1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/nexspence/values.yaml
git commit -m "feat(helm): add values.yaml with ingress and gateway defaults"
```

---

## Task 3: Core Kubernetes resources — Secret, ConfigMap, SA, PVC, Service

**Files:**
- Create: `deploy/helm/nexspence/templates/serviceaccount.yaml`
- Create: `deploy/helm/nexspence/templates/configmap.yaml`
- Create: `deploy/helm/nexspence/templates/secret.yaml`
- Create: `deploy/helm/nexspence/templates/pvc.yaml`
- Create: `deploy/helm/nexspence/templates/service.yaml`

- [ ] **Step 1: Write serviceaccount.yaml**

`deploy/helm/nexspence/templates/serviceaccount.yaml`:
```yaml
{{- if .Values.serviceAccount.create -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "nexspence.serviceAccountName" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
  {{- with .Values.serviceAccount.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
```

- [ ] **Step 2: Write configmap.yaml**

`deploy/helm/nexspence/templates/configmap.yaml`:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
data:
  NEXSPENCE_HTTP_LISTEN: {{ .Values.config.httpListen | quote }}
  NEXSPENCE_HTTP_BASE_URL: {{ .Values.config.baseURL | quote }}
  NEXSPENCE_LOG_LEVEL: {{ .Values.config.logLevel | quote }}
  NEXSPENCE_LOG_FORMAT: {{ .Values.config.logFormat | quote }}
  NEXSPENCE_AUTH_JWT_EXPIRY_HOURS: {{ .Values.config.jwtExpiryHours | quote }}
  NEXSPENCE_BOOTSTRAP_ADMIN_USERNAME: {{ .Values.config.adminUsername | quote }}
  NEXSPENCE_BOOTSTRAP_ADMIN_EMAIL: {{ .Values.config.adminEmail | quote }}
  NEXSPENCE_BOOTSTRAP_ADMIN_FIRST_NAME: {{ .Values.config.adminFirstName | quote }}
  {{- if eq .Values.storage.type "local" }}
  NEXSPENCE_STORAGE_LOCAL_BASE_PATH: {{ .Values.storage.local.mountPath | quote }}
  {{- else if eq .Values.storage.type "s3" }}
  NEXSPENCE_STORAGE_S3_ENDPOINT: {{ .Values.storage.s3.endpoint | quote }}
  NEXSPENCE_STORAGE_S3_BUCKET: {{ .Values.storage.s3.bucket | quote }}
  NEXSPENCE_STORAGE_S3_REGION: {{ .Values.storage.s3.region | quote }}
  NEXSPENCE_STORAGE_S3_USE_SSL: {{ .Values.storage.s3.useSSL | quote }}
  {{- end }}
```

- [ ] **Step 3: Write secret.yaml**

`deploy/helm/nexspence/templates/secret.yaml`:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
type: Opaque
stringData:
  NEXSPENCE_DATABASE_DSN: {{ include "nexspence.databaseDSN" . | quote }}
  NEXSPENCE_AUTH_JWT_SECRET: {{ .Values.config.jwtSecret | quote }}
  NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD: {{ .Values.config.adminPassword | quote }}
  {{- if eq .Values.storage.type "s3" }}
  NEXSPENCE_STORAGE_S3_ACCESS_KEY: {{ .Values.storage.s3.accessKey | quote }}
  NEXSPENCE_STORAGE_S3_SECRET_KEY: {{ .Values.storage.s3.secretKey | quote }}
  {{- end }}
```

- [ ] **Step 4: Write pvc.yaml**

`deploy/helm/nexspence/templates/pvc.yaml`:
```yaml
{{- if eq .Values.storage.type "local" }}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ include "nexspence.fullname" . }}-blobs
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: {{ .Values.storage.local.size }}
  {{- with .Values.storage.local.storageClass }}
  storageClassName: {{ . }}
  {{- end }}
{{- end }}
```

- [ ] **Step 5: Write service.yaml**

`deploy/helm/nexspence/templates/service.yaml`:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
  {{- with .Values.service.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  type: {{ .Values.service.type }}
  ports:
    - port: {{ .Values.service.port }}
      targetPort: {{ .Values.service.targetPort }}
      protocol: TCP
      name: http
  selector:
    {{- include "nexspence.selectorLabels" . | nindent 4 }}
```

- [ ] **Step 6: Verify templates render**

```bash
helm template test-release deploy/helm/nexspence 2>&1 | head -80
```

Expected: valid YAML output for SA, ConfigMap, Secret, PVC, Service — no errors.

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/nexspence/templates/serviceaccount.yaml \
        deploy/helm/nexspence/templates/configmap.yaml \
        deploy/helm/nexspence/templates/secret.yaml \
        deploy/helm/nexspence/templates/pvc.yaml \
        deploy/helm/nexspence/templates/service.yaml
git commit -m "feat(helm): add core k8s resource templates (sa, configmap, secret, pvc, service)"
```

---

## Task 4: Deployment template

**Files:**
- Create: `deploy/helm/nexspence/templates/deployment.yaml`

- [ ] **Step 1: Write deployment.yaml**

`deploy/helm/nexspence/templates/deployment.yaml`:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
spec:
  {{- if not .Values.autoscaling.enabled }}
  replicas: {{ .Values.replicaCount }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "nexspence.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
        checksum/secret: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}
        {{- with .Values.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
      labels:
        {{- include "nexspence.selectorLabels" . | nindent 8 }}
        {{- with .Values.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      {{- with .Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      serviceAccountName: {{ include "nexspence.serviceAccountName" . }}
      securityContext:
        {{- toYaml .Values.podSecurityContext | nindent 8 }}
      containers:
        - name: nexspence
          securityContext:
            {{- toYaml .Values.securityContext | nindent 12 }}
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - name: http
              containerPort: 8081
              protocol: TCP
          envFrom:
            - configMapRef:
                name: {{ include "nexspence.fullname" . }}
            - secretRef:
                name: {{ include "nexspence.fullname" . }}
          livenessProbe:
            {{- toYaml .Values.livenessProbe | nindent 12 }}
          readinessProbe:
            {{- toYaml .Values.readinessProbe | nindent 12 }}
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
          {{- if eq .Values.storage.type "local" }}
          volumeMounts:
            - name: blobs
              mountPath: {{ .Values.storage.local.mountPath }}
          {{- end }}
      {{- if eq .Values.storage.type "local" }}
      volumes:
        - name: blobs
          persistentVolumeClaim:
            claimName: {{ include "nexspence.fullname" . }}-blobs
      {{- end }}
      {{- with .Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
```

- [ ] **Step 2: Render and check output**

```bash
helm template test-release deploy/helm/nexspence | grep -A 60 "kind: Deployment"
```

Expected: a valid Deployment with `envFrom` pointing at the ConfigMap and Secret, and a `blobs` volume mount.

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/nexspence/templates/deployment.yaml
git commit -m "feat(helm): add Deployment template"
```

---

## Task 5: Ingress template — nginx / traefik / cilium ingress-controller

**Files:**
- Create: `deploy/helm/nexspence/templates/ingress.yaml`
- Create: `deploy/helm/nexspence/values-examples/nginx.yaml`
- Create: `deploy/helm/nexspence/values-examples/traefik.yaml`
- Create: `deploy/helm/nexspence/values-examples/cilium-ingress.yaml`

All three providers use standard `networking.k8s.io/v1 Ingress` — they differ only in `ingressClassName` and annotations.

- [ ] **Step 1: Write ingress.yaml**

`deploy/helm/nexspence/templates/ingress.yaml`:
```yaml
{{- if .Values.ingress.enabled -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
  {{- with .Values.ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- if .Values.ingress.className }}
  ingressClassName: {{ .Values.ingress.className }}
  {{- end }}
  {{- if .Values.ingress.tls }}
  tls:
    {{- toYaml .Values.ingress.tls | nindent 4 }}
  {{- end }}
  rules:
    {{- range .Values.ingress.hosts }}
    - host: {{ .host | quote }}
      http:
        paths:
          {{- range .paths }}
          - path: {{ .path }}
            pathType: {{ .pathType }}
            backend:
              service:
                name: {{ include "nexspence.fullname" $ }}
                port:
                  number: {{ $.Values.service.port }}
          {{- end }}
    {{- end }}
{{- end }}
```

- [ ] **Step 2: Write values-examples/nginx.yaml**

`deploy/helm/nexspence/values-examples/nginx.yaml`:
```yaml
# nginx ingress-controller example
ingress:
  enabled: true
  className: "nginx"
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: "10g"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "600"
    # Uncomment for TLS termination via cert-manager:
    # cert-manager.io/cluster-issuer: "letsencrypt-prod"
  hosts:
    - host: nexspence.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []
  # tls:
  #   - secretName: nexspence-tls
  #     hosts:
  #       - nexspence.example.com

config:
  baseURL: "https://nexspence.example.com"
  jwtSecret: "replace-with-64-char-random-secret"
  adminPassword: "replace-with-strong-password"
```

- [ ] **Step 3: Write values-examples/traefik.yaml**

`deploy/helm/nexspence/values-examples/traefik.yaml`:
```yaml
# Traefik ingress-controller example
ingress:
  enabled: true
  className: "traefik"
  annotations:
    # Route via websecure entrypoint (HTTPS)
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    # Increase body size limit (default Traefik has no limit, but explicit is safer)
    # traefik.ingress.kubernetes.io/buffering: |
    #   maxRequestBodyBytes = 10737418240
  hosts:
    - host: nexspence.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: nexspence-tls
      hosts:
        - nexspence.example.com

config:
  baseURL: "https://nexspence.example.com"
  jwtSecret: "replace-with-64-char-random-secret"
  adminPassword: "replace-with-strong-password"
```

- [ ] **Step 4: Write values-examples/cilium-ingress.yaml**

`deploy/helm/nexspence/values-examples/cilium-ingress.yaml`:
```yaml
# Cilium ingress-controller mode (uses standard K8s Ingress)
# Requires: Cilium >= 1.12 with ingress controller enabled in cilium-config
# kubectl -n kube-system patch configmap cilium-config --patch '{"data":{"enable-ingress-controller":"true"}}'
ingress:
  enabled: true
  className: "cilium"
  annotations:
    # Cilium LoadBalancer mode: "dedicated" (one LB per Ingress) or "shared"
    ingress.cilium.io/loadbalancer-mode: "dedicated"
  hosts:
    - host: nexspence.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []
  # tls:
  #   - secretName: nexspence-tls
  #     hosts:
  #       - nexspence.example.com

config:
  baseURL: "http://nexspence.example.com"
  jwtSecret: "replace-with-64-char-random-secret"
  adminPassword: "replace-with-strong-password"
```

- [ ] **Step 5: Render ingress with each example and verify**

```bash
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/nginx.yaml | grep -A 30 "kind: Ingress"
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/traefik.yaml | grep -A 30 "kind: Ingress"
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/cilium-ingress.yaml | grep -A 30 "kind: Ingress"
```

Expected: Each renders an `Ingress` with the correct `ingressClassName` and annotations.

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/nexspence/templates/ingress.yaml \
        deploy/helm/nexspence/values-examples/nginx.yaml \
        deploy/helm/nexspence/values-examples/traefik.yaml \
        deploy/helm/nexspence/values-examples/cilium-ingress.yaml
git commit -m "feat(helm): add ingress template (nginx/traefik/cilium) + example values"
```

---

## Task 6: Istio API Gateway — Gateway + VirtualService

**Files:**
- Create: `deploy/helm/nexspence/templates/istio-gateway.yaml`
- Create: `deploy/helm/nexspence/templates/istio-virtualservice.yaml`
- Create: `deploy/helm/nexspence/values-examples/istio-gateway.yaml`

Requires Istio CRDs (`networking.istio.io/v1beta1`) to be installed in the cluster. Templates are only rendered when `gateway.istio.enabled: true`.

- [ ] **Step 1: Write istio-gateway.yaml**

`deploy/helm/nexspence/templates/istio-gateway.yaml`:
```yaml
{{- if .Values.gateway.istio.enabled -}}
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: {{ include "nexspence.fullname" . }}-istio
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
spec:
  selector:
    {{- toYaml .Values.gateway.istio.gatewaySelector | nindent 4 }}
  servers:
    - port:
        number: {{ .Values.gateway.istio.port }}
        name: http
        protocol: {{ if .Values.gateway.istio.tlsMode }}HTTPS{{ else }}HTTP{{ end }}
      hosts:
        {{- toYaml .Values.gateway.istio.hosts | nindent 8 }}
      {{- if .Values.gateway.istio.tlsMode }}
      tls:
        mode: {{ .Values.gateway.istio.tlsMode }}
        {{- if .Values.gateway.istio.tlsSecret }}
        credentialName: {{ .Values.gateway.istio.tlsSecret }}
        {{- end }}
      {{- end }}
{{- end }}
```

- [ ] **Step 2: Write istio-virtualservice.yaml**

`deploy/helm/nexspence/templates/istio-virtualservice.yaml`:
```yaml
{{- if .Values.gateway.istio.enabled -}}
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: {{ include "nexspence.fullname" . }}-istio
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
  {{- with .Values.gateway.istio.vsAnnotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  hosts:
    {{- toYaml .Values.gateway.istio.hosts | nindent 4 }}
  gateways:
    - {{ include "nexspence.fullname" . }}-istio
  http:
    - match:
        - uri:
            prefix: /
      route:
        - destination:
            host: {{ include "nexspence.fullname" . }}
            port:
              number: {{ .Values.service.port }}
      timeout: {{ .Values.gateway.istio.timeout }}
{{- end }}
```

- [ ] **Step 3: Write values-examples/istio-gateway.yaml**

`deploy/helm/nexspence/values-examples/istio-gateway.yaml`:
```yaml
# Istio API Gateway example
# Prerequisites:
#   - Istio installed: istioctl install --set profile=default
#   - Namespace labeled: kubectl label namespace <ns> istio-injection=enabled
ingress:
  enabled: false

gateway:
  istio:
    enabled: true
    gatewaySelector:
      istio: ingressgateway
    port: 443
    tlsMode: "SIMPLE"
    # Secret must be in istio-system namespace (Istio reads certs from there)
    tlsSecret: "nexspence-tls"
    hosts:
      - "nexspence.example.com"
    timeout: "300s"
    vsAnnotations: {}

config:
  baseURL: "https://nexspence.example.com"
  jwtSecret: "replace-with-64-char-random-secret"
  adminPassword: "replace-with-strong-password"
```

- [ ] **Step 4: Render Istio templates and verify**

```bash
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/istio-gateway.yaml \
  | grep -A 30 "kind: Gateway"
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/istio-gateway.yaml \
  | grep -A 25 "kind: VirtualService"
```

Expected: `Gateway` with `protocol: HTTPS` + TLS section; `VirtualService` with host routing to nexspence service.

- [ ] **Step 5: Verify ingress is NOT rendered when istio is enabled**

```bash
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/istio-gateway.yaml \
  | grep "kind: Ingress" || echo "OK: no Ingress rendered"
```

Expected: `OK: no Ingress rendered`

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/nexspence/templates/istio-gateway.yaml \
        deploy/helm/nexspence/templates/istio-virtualservice.yaml \
        deploy/helm/nexspence/values-examples/istio-gateway.yaml
git commit -m "feat(helm): add Istio Gateway + VirtualService templates"
```

---

## Task 7: Cilium API Gateway — K8s Gateway API HTTPRoute

**Files:**
- Create: `deploy/helm/nexspence/templates/httproute.yaml`
- Create: `deploy/helm/nexspence/values-examples/cilium-gateway.yaml`

Uses the standard **Kubernetes Gateway API** (`gateway.networking.k8s.io/v1`) which Cilium implements via its `GatewayClass`. The `Gateway` resource is created per-release; `HTTPRoute` binds to it.

- [ ] **Step 1: Write httproute.yaml — creates both Gateway and HTTPRoute**

`deploy/helm/nexspence/templates/httproute.yaml`:
```yaml
{{- if .Values.gateway.cilium.enabled -}}
---
# K8s Gateway API Gateway (implemented by Cilium GatewayClass)
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: {{ include "nexspence.fullname" . }}-cilium
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
  {{- with .Values.gateway.cilium.gatewayAnnotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  gatewayClassName: {{ .Values.gateway.cilium.gatewayClassName }}
  listeners:
    {{- if .Values.gateway.cilium.tls }}
    - name: https
      protocol: HTTPS
      port: 443
      tls:
        mode: Terminate
        {{- with (index .Values.gateway.cilium.tls 0).certificateRefs }}
        certificateRefs:
          {{- toYaml . | nindent 10 }}
        {{- end }}
      allowedRoutes:
        namespaces:
          from: Same
    {{- else }}
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
    {{- end }}
---
# K8s Gateway API HTTPRoute
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{ include "nexspence.fullname" . }}-cilium
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
spec:
  parentRefs:
    - name: {{ include "nexspence.fullname" . }}-cilium
      namespace: {{ .Release.Namespace }}
  hostnames:
    {{- range .Values.gateway.cilium.hosts }}
    - {{ .host | quote }}
    {{- end }}
  rules:
    - matches:
        {{- range .Values.gateway.cilium.hosts }}
        {{- range .paths }}
        - path:
            type: {{ .pathType }}
            value: {{ .path }}
        {{- end }}
        {{- end }}
      backendRefs:
        - name: {{ include "nexspence.fullname" . }}
          port: {{ .Values.service.port }}
          weight: 100
{{- end }}
```

- [ ] **Step 2: Write values-examples/cilium-gateway.yaml**

`deploy/helm/nexspence/values-examples/cilium-gateway.yaml`:
```yaml
# Cilium API Gateway (K8s Gateway API) example
# Prerequisites:
#   - Cilium >= 1.14 with Gateway API enabled
#   - K8s Gateway API CRDs installed:
#     kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml
#   - GatewayClass "cilium" must exist (Cilium creates it automatically)
ingress:
  enabled: false

gateway:
  cilium:
    enabled: true
    gatewayClassName: "cilium"
    hosts:
      - host: nexspence.example.com
        paths:
          - path: /
            pathType: PathPrefix
    tls: []
    # tls:
    #   - certificateRefs:
    #       - kind: Secret
    #         name: nexspence-tls
    #     hosts:
    #       - nexspence.example.com
    gatewayAnnotations:
      # Request 10 GiB client body (for artifact uploads via Gateway)
      # Note: Cilium Gateway API 1.14+ respects this annotation
      cilium.io/lb-ipam-pool: "default"

config:
  baseURL: "http://nexspence.example.com"
  jwtSecret: "replace-with-64-char-random-secret"
  adminPassword: "replace-with-strong-password"
```

- [ ] **Step 3: Render and verify Gateway + HTTPRoute**

```bash
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  | grep -A 35 "kind: Gateway"
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  | grep -A 25 "kind: HTTPRoute"
```

Expected: `Gateway` with `gatewayClassName: cilium` + HTTP listener; `HTTPRoute` with `backendRefs` pointing at the nexspence service.

- [ ] **Step 4: Verify neither Ingress nor Istio resources render**

```bash
helm template test deploy/helm/nexspence -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  | grep -E "kind: (Ingress|VirtualService|networking.istio)" || echo "OK: clean"
```

Expected: `OK: clean`

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/nexspence/templates/httproute.yaml \
        deploy/helm/nexspence/values-examples/cilium-gateway.yaml
git commit -m "feat(helm): add K8s Gateway API templates for Cilium (Gateway + HTTPRoute)"
```

---

## Task 8: HPA template

**Files:**
- Create: `deploy/helm/nexspence/templates/hpa.yaml`

- [ ] **Step 1: Write hpa.yaml**

`deploy/helm/nexspence/templates/hpa.yaml`:
```yaml
{{- if .Values.autoscaling.enabled -}}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ include "nexspence.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "nexspence.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "nexspence.fullname" . }}
  minReplicas: {{ .Values.autoscaling.minReplicas }}
  maxReplicas: {{ .Values.autoscaling.maxReplicas }}
  metrics:
    {{- if .Values.autoscaling.targetCPUUtilizationPercentage }}
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetCPUUtilizationPercentage }}
    {{- end }}
    {{- if .Values.autoscaling.targetMemoryUtilizationPercentage }}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{ .Values.autoscaling.targetMemoryUtilizationPercentage }}
    {{- end }}
{{- end }}
```

- [ ] **Step 2: Render with autoscaling enabled**

```bash
helm template test deploy/helm/nexspence --set autoscaling.enabled=true \
  | grep -A 30 "kind: HorizontalPodAutoscaler"
```

Expected: valid HPA with CPU and memory metrics; note that `replicaCount` is not set in Deployment (controlled by HPA).

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/nexspence/templates/hpa.yaml
git commit -m "feat(helm): add HPA template"
```

---

## Task 9: Full helm lint across all example values

- [ ] **Step 1: Run helm lint for base chart**

```bash
helm lint deploy/helm/nexspence
```

Expected: `1 chart(s) linted, 0 chart(s) failed`

- [ ] **Step 2: Run helm lint with each example values file**

```bash
for f in deploy/helm/nexspence/values-examples/*.yaml; do
  echo "=== $f ==="
  helm lint deploy/helm/nexspence -f "$f"
done
```

Expected: all pass with `0 chart(s) failed`. Warnings about missing CRDs (Istio, Gateway API) are acceptable.

- [ ] **Step 3: Run helm template dry-run with each example to verify output**

```bash
for f in deploy/helm/nexspence/values-examples/*.yaml; do
  echo "=== $f ===" && helm template test deploy/helm/nexspence -f "$f" > /dev/null && echo "OK"
done
```

Expected: all 5 files print `OK`, no YAML parse errors.

- [ ] **Step 4: Run helm template with --debug for nginx example to view full output**

```bash
helm template test deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --debug 2>&1 | grep -E "^(---| {2}kind:| {4}name:)" | head -40
```

Expected: list of rendered resources: ServiceAccount, ConfigMap, Secret, PVC, Service, Deployment, Ingress.

- [ ] **Step 5: Commit lint results confirmation (no code changes needed)**

If all pass, proceed. If lint errors exist, fix the relevant template and re-run.

---

## Task 10: Chart README

**Files:**
- Create: `deploy/helm/nexspence/README.md`

- [ ] **Step 1: Write README.md**

`deploy/helm/nexspence/README.md`:
```markdown
# nexspence

Nexspence — open-source universal artifact repository manager (Nexus OSS alternative).

## TL;DR

```bash
helm repo add nexspence https://nexspence-oss.github.io/charts
helm install my-nexspence nexspence/nexspence -f your-values.yaml
```

## Prerequisites

- Kubernetes >= 1.26
- Helm >= 3.x
- PersistentVolume provisioner (for local blob storage) or S3-compatible storage

## Installing from local source

```bash
cd deploy/helm/nexspence
helm dependency update
helm install my-nexspence . -f values-examples/nginx.yaml
```

## Networking modes

Exactly **one** networking mode should be enabled at a time:

| Mode | values key | Requires |
|------|-----------|---------|
| nginx Ingress | `ingress.enabled=true`, `ingress.className=nginx` | nginx ingress-controller |
| Traefik Ingress | `ingress.enabled=true`, `ingress.className=traefik` | Traefik |
| Cilium Ingress | `ingress.enabled=true`, `ingress.className=cilium` | Cilium ingress enabled |
| Istio Gateway | `gateway.istio.enabled=true` | Istio + istiod |
| Cilium Gateway API | `gateway.cilium.enabled=true` | Cilium + Gateway API CRDs |

See `values-examples/` for ready-to-use per-provider files.

## Storage

| Mode | Config |
|------|-------|
| Local PVC | `storage.type=local` (default) |
| S3/MinIO | `storage.type=s3` + fill `storage.s3.*` |

> For multi-replica deployments, use S3 storage — a single PVC with `ReadWriteOnce` does not scale horizontally.

## Upgrading

```bash
helm upgrade my-nexspence . -f your-values.yaml
```

Database migrations run automatically on pod start.

## Values reference

See `values.yaml` — every key is annotated inline.
```

- [ ] **Step 2: Commit**

```bash
git add deploy/helm/nexspence/README.md
git commit -m "docs(helm): add chart README"
```

---

## Task 11: Update docs/deployment.md with Helm section

**Files:**
- Modify: `docs/deployment.md`

- [ ] **Step 1: Append Helm section to docs/deployment.md**

Add at the end of `docs/deployment.md`:

```markdown
---

## Helm (Kubernetes — recommended)

### Quick install (nginx ingress)

```bash
cd deploy/helm/nexspence
helm dependency update
helm install nexspence . \
  -f values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence --create-namespace
```

### Networking options

| Provider | Example values file |
|----------|-------------------|
| nginx ingress-controller | `values-examples/nginx.yaml` |
| Traefik ingress-controller | `values-examples/traefik.yaml` |
| Cilium ingress-controller | `values-examples/cilium-ingress.yaml` |
| Istio API Gateway | `values-examples/istio-gateway.yaml` |
| Cilium API Gateway (Gateway API) | `values-examples/cilium-gateway.yaml` |

### External PostgreSQL

```bash
helm install nexspence . \
  --set postgresql.enabled=false \
  --set externalDatabase.dsn="postgres://user:pass@pg-host:5432/nexspence" \
  -f values-examples/nginx.yaml
```

### S3 blob storage

```bash
helm install nexspence . \
  --set storage.type=s3 \
  --set storage.s3.endpoint="https://minio.example.com" \
  --set storage.s3.bucket="nexspence-blobs" \
  --set storage.s3.accessKey="minio" \
  --set storage.s3.secretKey="minio123" \
  -f values-examples/nginx.yaml
```

### Upgrading

```bash
helm upgrade nexspence . -f your-values.yaml
```

Migrations run automatically on pod restart.
```

- [ ] **Step 2: Commit**

```bash
git add docs/deployment.md
git commit -m "docs: add Helm deployment section to deployment.md"
```

---

## Task 12: Update task_plan.md with Phase 63

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 1: Append Phase 63 to task_plan.md**

Append after the Phase 62 entry:

```markdown
---

## Phase 63: Helm Chart
**Status:** planned

### Goal
Паковать Nexspence как production-ready Helm chart с поддержкой трёх провайдеров Ingress (nginx, traefik, cilium ingress-controller) и двух API Gateway (Istio, Cilium Gateway API via K8s Gateway API CRDs).

### Tasks
- [ ] Scaffold: `deploy/helm/nexspence/Chart.yaml`, `templates/_helpers.tpl`, `templates/NOTES.txt`
- [ ] `values.yaml` — все дефолты с аннотациями; секции ingress, gateway.istio, gateway.cilium, storage, autoscaling
- [ ] Core templates: ServiceAccount, ConfigMap, Secret, PVC, Service
- [ ] Deployment template с checksum annotations, probes, blob volume mount
- [ ] Ingress template (single file, works for nginx/traefik/cilium via ingressClassName + annotations)
- [ ] Istio Gateway + VirtualService templates (rendered only when gateway.istio.enabled=true)
- [ ] K8s Gateway API: Gateway + HTTPRoute templates for Cilium (rendered when gateway.cilium.enabled=true)
- [ ] HPA template
- [ ] 5 example values files (nginx, traefik, cilium-ingress, istio-gateway, cilium-gateway)
- [ ] Chart README
- [ ] Update docs/deployment.md with Helm section

**Files:** `deploy/helm/nexspence/`, `docs/deployment.md`, `task_plan.md`
```

- [ ] **Step 2: Commit**

```bash
git add task_plan.md
git commit -m "docs(task_plan): add Phase 63 Helm chart"
```

---

## Self-Review

### Spec coverage check

| Requirement | Covered in task |
|------------|----------------|
| Helm chart for Nexspence | Tasks 1–4 |
| nginx ingress support | Task 5 |
| Traefik ingress support | Task 5 |
| Cilium ingress support | Task 5 |
| Istio API Gateway | Task 6 |
| Cilium API Gateway | Task 7 |
| PostgreSQL sub-chart + external option | Task 2 (values.yaml) + Task 3 (secret DSN helper) |
| S3 storage option | Task 2 (values.yaml) + Task 4 (deployment mounts) |
| HPA | Task 8 |
| Lint validation | Task 9 |
| Documentation | Tasks 10–12 |

### Placeholder scan

No TBD, TODO, or "fill in details" present — all templates contain complete YAML.

### Type consistency

- `include "nexspence.fullname"` used consistently in all templates.
- `include "nexspence.databaseDSN"` defined in `_helpers.tpl` (Task 1) and used in `secret.yaml` (Task 3).
- `include "nexspence.selectorLabels"` used in both `service.yaml` and `deployment.yaml`.
- `gateway.cilium.hosts[*].host` referenced in `httproute.yaml` hostnames and matches the shape defined in `values.yaml`.
- `gateway.cilium.hosts[*].paths[*].pathType` values used as `PathPrefix` (Gateway API syntax, distinct from Ingress `Prefix`).
- `service.port` referenced in Ingress backend, Istio VirtualService destination, and HTTPRoute backendRefs — all consistent.
