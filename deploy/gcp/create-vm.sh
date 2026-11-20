#!/bin/sh
# Creates the PACT demo VM on Compute Engine. Run it in Google Cloud Shell (or
# anywhere gcloud is logged in), from the repository root:
#
#   sh deploy/gcp/create-vm.sh
#
# Optional settings: PROJECT (default: gcloud's current project), ZONE
# (us-central1-a), NAME (pact-demo), MACHINE (e2-standard-2).
# Safe to run again: anything that already exists is left as it is.
#
# What it creates:
#   $NAME-ip         a static IP, so the dashboard URL survives a stop and start
#   pact-allow-web   firewall rule: tcp 80 and 443 to VMs tagged pact-web, nothing else
#   $NAME            Ubuntu 24.04, 30 GB disk, and no service account: pact-core and
#                    the sink can reach the metadata server, so it holds no Google
#                    credentials for them to find
set -eu
cd "$(dirname "$0")/../.."

PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null || true)}
[ -n "$PROJECT" ] || { echo "No project set. Run: gcloud config set project YOUR_PROJECT_ID"; exit 1; }
ZONE=${ZONE:-us-central1-a}
REGION=${ZONE%-*}
NAME=${NAME:-pact-demo}
MACHINE=${MACHINE:-e2-standard-2}

echo "Project $PROJECT, zone $ZONE, VM $NAME ($MACHINE)"
gcloud services enable compute.googleapis.com --project="$PROJECT"

gcloud compute networks describe default --project="$PROJECT" >/dev/null 2>&1 || {
  echo "This project has no 'default' VPC network. Create one with:"
  echo "  gcloud compute networks create default --subnet-mode=auto --project=$PROJECT"
  echo "  gcloud compute firewall-rules create default-allow-ssh --network=default --allow=tcp:22 --project=$PROJECT"
  exit 1
}

if ! gcloud compute addresses describe "$NAME-ip" --region="$REGION" --project="$PROJECT" >/dev/null 2>&1; then
  gcloud compute addresses create "$NAME-ip" --region="$REGION" --project="$PROJECT"
fi
IP=$(gcloud compute addresses describe "$NAME-ip" --region="$REGION" --project="$PROJECT" --format='value(address)')

if ! gcloud compute firewall-rules describe pact-allow-web --project="$PROJECT" >/dev/null 2>&1; then
  gcloud compute firewall-rules create pact-allow-web --project="$PROJECT" \
    --network=default --direction=INGRESS --action=ALLOW --rules=tcp:80,tcp:443 \
    --source-ranges=0.0.0.0/0 --target-tags=pact-web \
    --description="PACT dashboard through Caddy (HTTPS and a password). 8080 and 9090 stay closed."
fi

if gcloud compute instances describe "$NAME" --zone="$ZONE" --project="$PROJECT" >/dev/null 2>&1; then
  echo "VM $NAME already exists; leaving it as it is."
else
  gcloud compute instances create "$NAME" --project="$PROJECT" --zone="$ZONE" \
    --machine-type="$MACHINE" \
    --image-family=ubuntu-2404-lts-amd64 --image-project=ubuntu-os-cloud \
    --boot-disk-size=30GB --boot-disk-type=pd-balanced \
    --address="$IP" --tags=pact-web \
    --no-service-account --no-scopes \
    --metadata-from-file=startup-script=deploy/gcp/startup.sh
fi

cat <<EOF

VM $NAME is at $IP. Its first boot installs Docker and Caddy (about 3 minutes).

Next, connect and follow docs/DEPLOY_GCP.md from step 2:
  gcloud compute ssh $NAME --zone=$ZONE --project=$PROJECT

Dashboard, once deploy/gcp/up.sh has run on the VM:
  https://$(echo "$IP" | tr . -).sslip.io
EOF
