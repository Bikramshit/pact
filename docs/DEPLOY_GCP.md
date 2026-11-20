# Deploy to Google Cloud

Run the whole stack on one Compute Engine VM, behind HTTPS and a password, so judges can open the dashboard from a link instead of a laptop.

## Why one VM

PACT's guarantee is the network layout: the agent's only network is `agent_net`, which is internal and reaches nothing but the broker ([How it works](HOW_IT_WORKS.md)). A VM running the same `docker compose` stack keeps that layout exactly, and `make bypass-test` passes there as it does on a laptop. Cloud Run or GKE would replace the Docker networks with something else and the bypass test would no longer prove the same thing.

```
internet --443--> Caddy (HTTPS, password) --> 127.0.0.1:8080  pact-core: control plane and dashboard
                                                     :9090  broker, on agent_net only, published nowhere
```

The control plane has no login of its own. Anyone who reaches it can sign contracts, approve held actions, reset the demo and spend Gemini quota, so it is only ever exposed through Caddy's password.

| Created | Purpose |
| --- | --- |
| VM `pact-demo` | `e2-standard-2` (2 vCPU, 8 GB), Ubuntu 24.04, 30 GB disk, no service account |
| Static IP `pact-demo-ip` | The URL stays the same when the VM is stopped and started |
| Firewall rule `pact-allow-web` | TCP 80 and 443 only. 8080 and 9090 are never opened |

The scripts are in [`deploy/gcp/`](../deploy/gcp/).

## Before you start

- A Google Cloud project with billing enabled.
- **Cloud Shell**: open [console.cloud.google.com](https://console.cloud.google.com) and press the terminal icon at the top right. `gcloud` is installed and logged in there; nothing is needed on your laptop.
- The project's `default` VPC network, which new projects have. `create-vm.sh` tells you how to create it if it is missing.
- The `deploy/gcp/` files pushed to GitHub, since both Cloud Shell and the VM clone the repository.
- A Gemini API key is optional, as on a laptop.

## 1. Create the VM (Cloud Shell)

```sh
gcloud config set project YOUR_PROJECT_ID
git clone https://github.com/Bikramshit/pact.git && cd pact
sh deploy/gcp/create-vm.sh
```

To choose another zone or size: `ZONE=europe-west1-b MACHINE=e2-standard-4 sh deploy/gcp/create-vm.sh`. The script is safe to run again.

It prints the VM's IP and the dashboard URL, `https://<ip-with-dashes>.sslip.io`. sslip.io resolves that name to the IP, so Caddy can get a Let's Encrypt certificate without a domain of your own. The first boot installs Docker and Caddy, which takes about 3 minutes.

## 2. Connect

```sh
gcloud compute ssh pact-demo --zone=us-central1-a
```

Wait until the first boot has finished. This ends with `pact: VM ready`; press Ctrl-C after it:

```sh
sudo journalctl -u google-startup-scripts -f
```

## 3. Start PACT (on the VM)

```sh
git clone https://github.com/Bikramshit/pact.git && cd pact
cp .env.example .env
nano .env                 # optional: paste GEMINI_API_KEY, then Ctrl-O, Enter, Ctrl-X
sh deploy/gcp/up.sh
```

The first run adds you to the `docker` group and stops. Type `exit`, connect again as in step 2, then run `cd pact && sh deploy/gcp/up.sh` again.

`up.sh` asks for a dashboard password (10 or more characters; the user name is `judge`), then:

1. Replaces the public dev values of `TARGET_TOKEN`, `RUNNER_TOKEN` and `POSTGRES_PASSWORD` in `.env` with random ones, and makes `.env` readable by you only.
2. Writes the Caddy configuration: HTTPS for the sslip.io name, the password, then `127.0.0.1:8080`.
3. Builds and starts the stack. The first build takes about 10 minutes.
4. Checks that pact-core is healthy, that the HTTPS URL answers with the password, and that it refuses requests without one (401).

## 4. Check it

On the VM:

```sh
make bypass-test          # the agent has no route around the broker
make replay               # every scripted scenario gets its expected decision
```

In a private browser window, open the URL, sign in as `judge` and run the HERO task from **Run a task**. The flow diagram should update live and the approval dialog should open for the pipeline edit.

From Cloud Shell, this should time out, because port 8080 is not open to the internet:

```sh
curl -m 5 http://YOUR_VM_IP:8080/healthz
```

## Demo day

| Task | Command |
| --- | --- |
| Stop the VM when not in use | `gcloud compute instances stop pact-demo --zone=us-central1-a` |
| Start it again | `gcloud compute instances start pact-demo --zone=us-central1-a` |
| Pick up new code (on the VM) | `cd pact && git pull && make build && make up` |
| Change the password (on the VM) | `sh deploy/gcp/up.sh` |
| Tamper and restore the chain (on the VM) | `make tamper`, `make untamper` |
| Logs (on the VM) | `make logs`, `sudo journalctl -u caddy -n 50` |

After a start, the containers and Caddy come back by themselves, on the same IP and URL. The evidence chain and contracts are in Docker volumes and survive the stop. Before going on stage, run the checklist in the [README](../README.md#before-going-on-stage) against the VM, with `make` commands in an SSH session.

## Cost

Rough on-demand prices for `us-central1`; check the [pricing calculator](https://cloud.google.com/products/calculator) for your region.

| State | About |
| --- | --- |
| Running | $0.07 per hour for the VM (about $50 a month if left on), plus the disk and IP below |
| Stopped | $3 a month for the 30 GB disk, plus about $0.01 per hour for the reserved IP |

Gemini calls are billed to the key's project separately.

## Remove it

From Cloud Shell:

```sh
gcloud compute instances delete pact-demo --zone=us-central1-a
gcloud compute addresses delete pact-demo-ip --region=us-central1
gcloud compute firewall-rules delete pact-allow-web
```

## Security notes

- **Only 80 and 443 are open.** Port 80 only redirects to HTTPS and answers the certificate challenge. Compose binds 8080 to the VM's loopback, and 9090 is published nowhere.
- **Every request needs the password**, including the API and the live event stream. Share it with judges only. It is one shared password, not a per-person login.
- **The VM has no service account.** pact-core and the sink sit on `egress_net` and can reach the Compute Engine metadata server. With no service account there are no Google credentials for them to find. The agent and runner networks are internal and cannot reach it at all.
- **Secrets stay on the VM.** `.env` is mode 600 and never leaves the VM. The Gemini key is not put in instance metadata, which every container on `egress_net` could read.
- **The trust assumption is the same as on a laptop.** Whoever controls the VM controls pact-core and its local signing keys ([Threat model](THREAT_MODEL.md)). To move the org and issuer keys into Cloud KMS, follow the README's Cloud KMS section, and give the VM a dedicated service account that has only `roles/cloudkms.signerVerifier` on the `pact` keyring.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `gcloud compute ssh` times out | The network has no SSH rule. Run `gcloud compute firewall-rules create pact-allow-ssh --network=default --allow=tcp:22 --source-ranges=35.235.240.0/20` and connect with `--tunnel-through-iap` |
| `up.sh` says the VM is still installing, long after boot | The startup script failed. Read `sudo journalctl -u google-startup-scripts`, then rerun it with `sudo google_metadata_script_runner startup` |
| `up.sh` says the HTTPS URL does not answer | Caddy could not get a certificate. Read `sudo journalctl -u caddy -n 50`. Port 80 must be reachable (the `pact-allow-web` rule). If sslip.io hits Let's Encrypt's rate limit, point your own domain's A record at the VM's IP and run `PACT_HOST=demo.example.com sh deploy/gcp/up.sh` |
| 502 Bad Gateway in the browser | pact-core is not running. Check `make ps` and `make logs` |
| The build runs out of memory | Recreate the VM with `MACHINE=e2-standard-4` |
| The repository is private, so the VM cannot clone it | On your laptop: `git archive --format=tar.gz -o pact.tgz HEAD`, then `gcloud compute scp pact.tgz pact-demo:~ --zone=us-central1-a`. On the VM: `mkdir pact && tar -xzf pact.tgz -C pact` |
