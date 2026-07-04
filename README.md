# DNS Controller

DNS Controller keeps Cloudflare A records synchronized with the current public IP address.

## Configuration

Provide one or more fully qualified domain names and exactly one Cloudflare authentication method.

For a scoped API token:

```sh
dns-controller \
  --records home.example.com,vpn.example.com \
  --cloudflare-api-token TOKEN
```

For a global API key:

```sh
dns-controller \
  --records home.example.com,vpn.example.com \
  --cloudflare-api-key KEY \
  --cloudflare-email owner@example.com
```

Flags are also read from uppercase, underscore-separated environment variables by the shared application runtime:

```sh
docker run --rm \
  --env RECORDS=home.example.com,vpn.example.com \
  --env CLOUDFLARE_API_TOKEN=TOKEN \
  justinswe/dns-controller:latest
```

The supported variables are `RECORDS`, `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_API_KEY`, and `CLOUDFLARE_EMAIL`. Do not supply the email variable when using an API token.

## Publishing

Increment the version in `MODULE.bazel`
