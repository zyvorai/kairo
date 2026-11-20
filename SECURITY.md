# Security policy

Kairo processes Kubernetes manifests, which can contain sensitive configuration. Treat snapshots as confidential infrastructure data.

## Safe deployment defaults

- Do not include Secret values in snapshots. Kairo does not need them.
- Run the API on a private network until authentication and authorization are configured for your environment.
- Terminate TLS at a trusted ingress/reverse proxy or add TLS directly to the server.
- Apply request-size and network-policy controls. The server already limits simulation request bodies to 4 MiB.
- Do not expose production cluster snapshots in public CI logs or pull-request comments.

## Reporting vulnerabilities

Please report suspected vulnerabilities privately to the project maintainers rather than opening a public issue with exploit details.
