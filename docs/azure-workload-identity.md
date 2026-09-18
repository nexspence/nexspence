# Azure Blob Storage with AKS Workload Identity

This guide configures Nexspence to access an existing Azure Blob container
with a user-assigned managed identity. No account key, connection string or SAS
token is stored in the Helm values or in the Nexspence Secret. The example is
for AKS; the same projected-token mechanism works for other Kubernetes
clusters that expose an OIDC issuer and run the Azure Workload Identity
webhook.

The chart example is
[`values-examples/azure-workload-identity.yaml`](../deploy/helm/nexspence/values-examples/azure-workload-identity.yaml).

## Prerequisites

You need an AKS cluster, an existing storage account and an existing blob
container. The cluster must have the OIDC issuer and workload identity enabled:

```bash
RESOURCE_GROUP="my-resource-group"
AKS_NAME="my-aks"
NAMESPACE="nexspence"
SERVICE_ACCOUNT_NAME="nexspence-azure"
STORAGE_ACCOUNT="mystorageaccount"
CONTAINER_NAME="nexspence-blobs"

az aks update \
  --resource-group "$RESOURCE_GROUP" \
  --name "$AKS_NAME" \
  --enable-oidc-issuer \
  --enable-workload-identity

OIDC_ISSUER=$(az aks show \
  --resource-group "$RESOURCE_GROUP" \
  --name "$AKS_NAME" \
  --query 'oidcIssuerProfile.issuerUrl' -o tsv)
```

AKS publishes the issuer URL; it must be used exactly as returned, including
the `https://` scheme. Microsoft documents these cluster prerequisites in the
[AKS Workload Identity overview](https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview)
and the [AKS deployment guide](https://learn.microsoft.com/en-us/azure/aks/workload-identity-deploy-cluster).

## Create the identity and federation

Create a user-assigned managed identity. The federated credential binds only
the Nexspence ServiceAccount in the selected namespace to that identity:

```bash
IDENTITY_NAME="nexspence-azure"
IDENTITY_RESOURCE_GROUP="$RESOURCE_GROUP"

az identity create \
  --resource-group "$IDENTITY_RESOURCE_GROUP" \
  --name "$IDENTITY_NAME"

CLIENT_ID=$(az identity show \
  --resource-group "$IDENTITY_RESOURCE_GROUP" \
  --name "$IDENTITY_NAME" \
  --query clientId -o tsv)
IDENTITY_PRINCIPAL_ID=$(az identity show \
  --resource-group "$IDENTITY_RESOURCE_GROUP" \
  --name "$IDENTITY_NAME" \
  --query principalId -o tsv)
TENANT_ID=$(az account show --query tenantId -o tsv)

az identity federated-credential create \
  --resource-group "$IDENTITY_RESOURCE_GROUP" \
  --identity-name "$IDENTITY_NAME" \
  --name nexspence-azure \
  --issuer "$OIDC_ISSUER" \
  --subject "system:serviceaccount:${NAMESPACE}:${SERVICE_ACCOUNT_NAME}" \
  --audiences api://AzureADTokenExchange
```

The subject must match the rendered ServiceAccount name and namespace. The
audience `api://AzureADTokenExchange` is the audience expected by Azure
Workload Identity.

## Assign Azure data permissions

Assign `Storage Blob Data Contributor` to the identity at the container scope.
This grants Nexspence the blob read, write and delete operations it needs while
keeping the permission narrower than the storage account:

```bash
STORAGE_ID=$(az storage account show \
  --resource-group "$RESOURCE_GROUP" \
  --name "$STORAGE_ACCOUNT" \
  --query id -o tsv)
CONTAINER_SCOPE="${STORAGE_ID}/blobServices/default/containers/${CONTAINER_NAME}"

az role assignment create \
  --assignee-object-id "$IDENTITY_PRINCIPAL_ID" \
  --assignee-principal-type ServicePrincipal \
  --role "Storage Blob Data Contributor" \
  --scope "$CONTAINER_SCOPE"
```

If Nexspence must generate presigned URLs with Entra ID, it requests a user
delegation key. Assign `Storage Blob Delegator` at least at the storage-account
scope as well; a container-scoped `Storage Blob Data Contributor` assignment
does not grant the user-delegation-key action:

```bash
az role assignment create \
  --assignee-object-id "$IDENTITY_PRINCIPAL_ID" \
  --assignee-principal-type ServicePrincipal \
  --role "Storage Blob Delegator" \
  --scope "$STORAGE_ID"
```

See Microsoft's [Azure Storage built-in roles](https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles/storage)
for the role actions and [Create a user delegation SAS](https://learn.microsoft.com/en-us/rest/api/storageservices/create-user-delegation-sas)
for the delegation-key requirement. The Delegator assignment is not needed
when Nexspence never creates presigned URLs, or when another credential mode
(account key or a pre-generated SAS) is used.

## Install and verify

Review the example, replace the IDs and host name, then render it before
installing:

```bash
helm lint deploy/helm/nexspence
helm template nexspence deploy/helm/nexspence \
  --namespace "$NAMESPACE" \
  -f deploy/helm/nexspence/values-examples/azure-workload-identity.yaml

helm install nexspence deploy/helm/nexspence \
  --namespace "$NAMESPACE" \
  --create-namespace \
  -f deploy/helm/nexspence/values-examples/azure-workload-identity.yaml
```

The rendered Deployment must contain
`azure.workload.identity/use: "true"` under `spec.template.metadata.labels`.
The rendered ServiceAccount must contain
`azure.workload.identity/client-id` and
`azure.workload.identity/tenant-id`. Nexspence's Azure configuration must show
the container and account name, with all three credential values empty.

Check the pod and its projected identity configuration:

```bash
kubectl -n "$NAMESPACE" get serviceaccount "$SERVICE_ACCOUNT_NAME" -o yaml
kubectl -n "$NAMESPACE" get pod -l app.kubernetes.io/instance=nexspence -o yaml
kubectl -n "$NAMESPACE" logs deploy/nexspence
```

The first startup performs a container-properties check. A `403` usually means
the data role is missing or not propagated yet; a user-delegation-SAS failure
requires the additional `Storage Blob Delegator` assignment. Role changes can
take a few minutes to propagate.

A startup that fails with `azure blob store sync failed: refusing automatic
Azure blob-store target change` is not a permissions problem. It means this is
an existing installation whose `default` or `docker` blob store already holds
data, so Nexspence will not silently repoint it at the container — see
[Switching an existing installation to Azure](deployment.md#switching-an-existing-installation-to-azure)
for the migration path. A fresh install never hits this.

Never put account keys, connection strings or SAS tokens in this example or in
a committed values file. Workload Identity is the intended credential path for
this deployment.
