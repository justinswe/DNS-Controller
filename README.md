<h1 align="center">DNS Controller</h1>

<p align="center">
  A small, open-source dynamic DNS updater that keeps Cloudflare A records pointed at your current public IP address.
</p>

<p align="center">
  <img alt="Publish passing" src="https://img.shields.io/badge/publish-passing-brightgreen">
  <a href="https://hub.docker.com/r/justinswe/dns-controller"><img alt="Docker pulls" src="https://img.shields.io/docker/pulls/justinswe/dns-controller"></a>
  <a href="https://hub.docker.com/r/justinswe/dns-controller/tags"><img alt="Docker image version" src="https://img.shields.io/docker/v/justinswe/dns-controller?sort=semver"></a>
  <a href="https://github.com/justinswe/DNS-Controller/blob/main/LICENSE"><img alt="MIT license" src="https://img.shields.io/github/license/justinswe/DNS-Controller"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="https://hub.docker.com/r/justinswe/dns-controller">Docker Hub</a>
</p>

Home networks and self-hosted services rarely get a static public address. DNS Controller resolves the address you actually have and writes it to the Cloudflare A records you name, so `vpn.example.com` keeps resolving after your ISP hands you a new lease.

## Features

| Capability | What it does |
| --- | --- |
| **Cloudflare A records** | Manages one or more FQDNs per run through the Cloudflare v4 REST API. Records are written with a 300-second TTL and `proxied: false`. |
| **Create or update** | Creates the A record when it does not exist yet, updates it when the address changed, and makes no write at all when the record already holds the current IP. |
| **Two auth methods** | A scoped API token, or a global API key paired with the account email. Exactly one method is accepted. |
| **Flags or environment** | Every flag is also read from an uppercase, underscore-separated environment variable by the shared `github.com/justinswe/std/app` runtime, so the same binary configures cleanly from a shell or a container. |
| **One-shot by design** | Performs a single reconciliation and exits, so scheduling stays with whatever already runs on a timer: cron, systemd, or a Kubernetes `CronJob`. No daemon, no internal ticker. |
| **Small image** | A pure-Go static binary on a digest-pinned `distroless/base-debian13:nonroot` base, running as a non-root user. Published for `linux/amd64`. |

## Quick start

You need a Cloudflare API token with **Zone:Read** and **DNS:Edit** permissions on the zones holding your records, and those zones must already exist in your Cloudflare account.

Pull the published image from [Docker Hub](https://hub.docker.com/r/justinswe/dns-controller):

```sh
docker pull justinswe/dns-controller:latest
```

Create a local `dns-controller.env` file:

```dotenv
RECORDS=home.example.com,vpn.example.com
CLOUDFLARE_API_TOKEN=YOUR_CLOUDFLARE_API_TOKEN
```

Keep this file out of source control. Then run it:

```sh
docker run --rm \
  --env-file ./dns-controller.env \
  justinswe/dns-controller:latest
```

The container performs one reconciliation, logs what it changed, and exits zero. Any failure — an unreachable IP service, a zone that does not exist, a rejected credential — aborts the run with a non-zero exit code before the remaining records are processed.

Because the process exits, schedule it. Every fifteen minutes from cron:

```cron
*/15 * * * * docker run --rm --env-file /etc/dns-controller.env justinswe/dns-controller:latest
```

Or as a Kubernetes `CronJob`, with the token in a Secret:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: dns-controller
spec:
  schedule: "*/15 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: dns-controller
              image: justinswe/dns-controller:latest
              env:
                - name: RECORDS
                  value: home.example.com,vpn.example.com
                - name: CLOUDFLARE_API_TOKEN
                  valueFrom:
                    secretKeyRef:
                      name: cloudflare
                      key: api-token
```

## How it works

```text
api.ipify.org --> dns-controller --> Cloudflare API v4
                                     GET  /zones?name=<apex>
                                     GET  /zones/{id}/dns_records?type=A&name=<fqdn>
                                     POST /zones/{id}/dns_records          (record absent)
                                     PUT  /zones/{id}/dns_records/{rec}    (address changed)
```

One run resolves the public IP address once from `api.ipify.org`, then processes each configured FQDN in order: derive the apex domain, look up its zone ID by name, look for an existing A record with that exact name, and create it, update it, or leave it alone. Requests carry a 15-second HTTP client timeout.

> [!NOTE]
> The apex domain is derived as the last two labels of the FQDN. That is correct for `vpn.example.com`, but wrong for multi-part public suffixes: `vpn.example.co.uk` resolves to `co.uk`, and the zone lookup then fails rather than silently writing to the wrong zone.

## Configuration

| Variable | Required | Purpose |
| --- | --- | --- |
| `RECORDS` | Yes | Comma-separated A record FQDNs to manage. The `--records` flag is also repeatable. |
| `CLOUDFLARE_API_TOKEN` | One auth method | Scoped API token, sent as a bearer token. |
| `CLOUDFLARE_API_KEY` | One auth method | Global API key, sent with the account email. |
| `CLOUDFLARE_EMAIL` | With an API key | Cloudflare account email. Required with a global API key, and rejected with a token. |

Every non-repeatable flag is also available as an uppercase environment variable with hyphens replaced by underscores — `--cloudflare-api-token` maps to `CLOUDFLARE_API_TOKEN`. Run with `--help` to see all options, or `version` to print the build version.

Startup validates that exactly one authentication method is configured. Supplying both a token and a key, neither, a key without an email, or a token together with an email all fail before any request is made. Prefer a scoped token over the global API key: the key grants full account access, while a token can be limited to the zones and permissions this tool actually needs.

Equivalent invocations without a container:

```sh
dns-controller \
  --records home.example.com,vpn.example.com \
  --cloudflare-api-token YOUR_CLOUDFLARE_API_TOKEN
```

```sh
dns-controller \
  --records home.example.com,vpn.example.com \
  --cloudflare-api-key YOUR_CLOUDFLARE_API_KEY \
  --cloudflare-email owner@example.com
```

## Run from source

The build is Bazel-only and hermetic; no local Go toolchain is required.

```sh
bazel run //:dns-controller -- --help
```

Build the container image into your local Docker daemon:

```sh
bazel run //:image_load
```

## Development

Run the test suite:

```sh
bazel test //...
```

Presubmit and publishing both run on [BuildBuddy](https://buildbuddy.io) via `buildbuddy.yaml`; there are no GitHub Actions. Pull requests to `main` are tested with remote build execution and must bump `version` in `MODULE.bazel` to a newer canonical `MAJOR.MINOR.PATCH` — presubmit fails otherwise. Merging to `main` re-runs the tests and then publishes `justinswe/dns-controller:<version>` and `justinswe/dns-controller:latest`, so the module version is the release version.

## License

DNS Controller is available under the [MIT License](LICENSE).
