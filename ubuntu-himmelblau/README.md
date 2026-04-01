# ubuntu-himmelblau

Containerfile and build scripts for the Himmelblau-based container image used by intuneme.

This image uses [Himmelblau](https://github.com/himmelblau-idm/himmelblau) as the authentication
and enrollment stack instead of Microsoft Identity Broker + Intune Portal. It replaces the
Intune/identity-broker D-Bus services with Himmelblau's own broker, which implements the same
`com.microsoft.identity.broker1` D-Bus interface.

Microsoft Edge is still included for users who need it.

## Differences from ubuntu-intune

| Component | ubuntu-intune | ubuntu-himmelblau |
|---|---|---|
| Auth daemon | microsoft-identity-broker | himmelblaud + himmelblaud-tasks |
| Enrollment | intune-portal | aad-tool (CLI) |
| Host SSO broker | microsoft-identity-broker (D-Bus) | himmelblau-broker (D-Bus) |
| Browser | Microsoft Edge | Microsoft Edge |
| Password policy | pam_pwquality (container-enforced) | Himmelblau (server-enforced) |

## Building locally

```bash
podman build -f ubuntu-himmelblau/Containerfile -t ubuntu-himmelblau:latest .
```
