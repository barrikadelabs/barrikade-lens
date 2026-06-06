#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────
# Barrikade Lens API — GCP Setup Script
#
# Prerequisites:
#   1. gcloud CLI installed and authenticated
#   2. GCP project "barrikade" exists and is active
#
# Usage:
#   chmod +x setup/deploy.sh
#   ./setup/deploy.sh
# ──────────────────────────────────────────────────────────────
set -euo pipefail

PROJECT_ID="barrikade"
REGION="us-central1"
SERVICE_NAME="barrikade-lens-api"
DATASET="lens"
TABLE="telemetry_events"
IMAGE="gcr.io/${PROJECT_ID}/${SERVICE_NAME}"

echo "══════════════════════════════════════════════════════════"
echo "  Barrikade Lens API — GCP Deployment"
echo "══════════════════════════════════════════════════════════"
echo ""

# ── Step 1: Set project ───────────────────────────────────────
echo "→ Setting active project to ${PROJECT_ID}..."
gcloud config set project "${PROJECT_ID}"

# ── Step 2: Enable required APIs ──────────────────────────────
echo "→ Enabling Cloud Run and BigQuery APIs..."
gcloud services enable run.googleapis.com bigquery.googleapis.com cloudbuild.googleapis.com containerregistry.googleapis.com

# ── Step 3: Create BigQuery dataset + table ───────────────────
echo "→ Creating BigQuery dataset '${DATASET}'..."
bq --project_id="${PROJECT_ID}" mk --dataset --location=US "${PROJECT_ID}:${DATASET}" 2>/dev/null || echo "  (dataset already exists)"

echo "→ Creating BigQuery table '${TABLE}'..."
bq query --project_id="${PROJECT_ID}" --use_legacy_sql=false < "$(dirname "$0")/schema.sql" 2>/dev/null || echo "  (table already exists)"

# ── Step 4: Build and push Docker image ───────────────────────
echo "→ Building Docker image..."
cd "$(dirname "$0")/.."
gcloud builds submit --tag "${IMAGE}:latest" .

# ── Step 5: Deploy to Cloud Run ───────────────────────────────
echo "→ Deploying to Cloud Run..."
gcloud run deploy "${SERVICE_NAME}" \
  --image "${IMAGE}:latest" \
  --region "${REGION}" \
  --platform managed \
  --allow-unauthenticated \
  --memory 256Mi \
  --min-instances 0 \
  --max-instances 3 \
  --set-env-vars "NODE_ENV=production"

# ── Step 6: Grant BigQuery access to Cloud Run SA ─────────────
echo "→ Granting BigQuery dataEditor to Cloud Run service account..."
SA_EMAIL=$(gcloud run services describe "${SERVICE_NAME}" --region="${REGION}" --format='value(spec.template.spec.serviceAccountName)')
if [ -z "${SA_EMAIL}" ]; then
  # Falls back to default compute SA
  PROJECT_NUMBER=$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')
  SA_EMAIL="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
fi

bq add-iam-policy-binding \
  --member="serviceAccount:${SA_EMAIL}" \
  --role="roles/bigquery.dataEditor" \
  "${PROJECT_ID}:${DATASET}" 2>/dev/null || \
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
  --member="serviceAccount:${SA_EMAIL}" \
  --role="roles/bigquery.dataEditor" \
  --condition=None

# ── Step 7: Map custom domain ─────────────────────────────────
echo ""
echo "══════════════════════════════════════════════════════════"
echo "  ✔  Deployment complete!"
echo ""
echo "  Cloud Run URL:"
gcloud run services describe "${SERVICE_NAME}" --region="${REGION}" --format='value(status.url)'
echo ""
echo "  Next steps:"
echo "    1. Map custom domain:"
echo "       gcloud beta run domain-mappings create \\"
echo "         --service ${SERVICE_NAME} \\"
echo "         --domain api.barrikade.ai \\"
echo "         --region ${REGION}"
echo ""
echo "    2. In Cloudflare, add a CNAME record:"
echo "       Name:   api"
echo "       Target: ghs.googlehosted.com"
echo "       Proxy:  DNS only (grey cloud)"
echo ""
echo "    3. Wait ~15 min for SSL provisioning, then test:"
echo "       curl https://api.barrikade.ai/lens/health"
echo "══════════════════════════════════════════════════════════"
