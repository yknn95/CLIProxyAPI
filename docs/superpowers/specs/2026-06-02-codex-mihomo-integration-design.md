# Codex-Only Integration and Mihomo Routing Design

Date: 2026-06-02

## Goal

Integrate and simplify the existing CLIProxyAPI, Cli-Proxy-API-Management-Center, and mihomo setup so the product focuses on Codex account management and Codex proxy execution.

The first implementation phase uses a mihomo child-process supervisor managed by CLIProxyAPI. CLIProxyAPI owns mihomo startup, generated configuration, health checks, shutdown, and account-to-route selection. Directly importing mihomo as an in-process Go library is reserved as a later option only if the child-process model proves insufficient.

## Non-Goals

- Do not remove or change the public core proxy API surface.
- Do not directly vendor or import mihomo internals in the first phase.
- Do not add per-account scheduler weight. The scheduler weight is a global repeat count.
- Do not require users to manually start mihomo before starting CLIProxyAPI.
- Do not silently fall back when a configured non-DIRECT mihomo policy is missing.

## Configuration

CLIProxyAPI adds three configuration sections.

```yaml
mihomo:
  enabled: true
  binary: /src/mihomo/mihomo
  config: /src/mihomo/deploy/config.yaml
  work-dir: /src/mihomo/deploy
  generated-config: ./data/mihomo.generated.yaml
  mixed-port-start: 7897
  controller: 127.0.0.1:9090
  health-timeout: 10s

codex-routing:
  default-policy: DIRECT
  account-routes:
    - tpo@xiaozhigame.com,🇯🇵 Japan 01
    - tpo1@xiaozhigame.com,Japan-LB
    - tpo2@xiaozhigame.com,DIRECT

codex-scheduler:
  weight: 2
```

`account-routes` also supports structured entries:

```yaml
codex-routing:
  account-routes:
    - email: tpo@xiaozhigame.com
      policy: 🇯🇵 Japan 01
```

Configuration semantics:

- `mihomo.enabled` enables CLIProxyAPI-managed mihomo startup.
- `mihomo.binary` points to the mihomo executable.
- `mihomo.config` is the source mihomo configuration.
- `mihomo.generated-config` is the generated runtime configuration used by the child process.
- `mihomo.mixed-port-start` is the first local port assigned to generated per-policy inbound proxies.
- `codex-routing.default-policy` is used when an account has no email or the email is not listed.
- `DIRECT` means no proxy is set for the Codex upstream request.
- Non-DIRECT policies must exist in the mihomo source configuration as a proxy or proxy-group name.
- Account email matching uses trim plus lower-case normalization.
- `codex-scheduler.weight` is a global repeat count. The default is `2`.

## Mihomo Supervisor

CLIProxyAPI starts a new mihomo supervisor during service startup when `mihomo.enabled` is true.

Supervisor responsibilities:

1. Read and validate the source mihomo configuration.
2. Collect all non-DIRECT policies from `codex-routing.default-policy` and `codex-routing.account-routes`.
3. Validate that every collected policy exists in the mihomo configuration.
4. Generate a runtime mihomo configuration with one isolated local inbound per policy.
5. Start mihomo as a child process with the generated configuration.
6. Check mihomo health through the configured controller or local proxy readiness.
7. Expose policy-to-proxy URL mappings to CLIProxyAPI.
8. Stop the child process during CLIProxyAPI shutdown.

The supervisor does not modify the mihomo source repository or source config file. It writes only the generated runtime config path configured by CLIProxyAPI.

## Per-Policy Inbound Routing

Each non-DIRECT mihomo policy receives a dedicated local inbound port. CLIProxyAPI uses those generated local ports as per-account `proxy-url` values.

Example policy mapping:

```text
Japan-LB      -> http://127.0.0.1:7897
🇯🇵 Japan 01  -> http://127.0.0.1:7898
```

This design avoids global selector switching. Concurrent Codex requests from different accounts can use different policies without mutating shared mihomo state.

The generated mihomo configuration must force traffic arriving on each generated inbound to the matching policy. The implementation should prefer mihomo-supported listener and rule mechanisms already available in the local mihomo version. If the local mihomo version does not support per-inbound rule matching, implementation should fail fast with a clear startup error instead of using global selector switching.

## Account Routing

Codex account routing happens after the scheduler chooses an account and before the Codex upstream executor creates its HTTP transport.

Routing flow:

1. Resolve the account email from OAuth file metadata.
2. If config-backed Codex credentials are used, support an optional `email` field for routing only.
3. Normalize the email with trim plus lower-case.
4. Look up the email in `codex-routing.account-routes`.
5. If no email exists or no route matches, use `codex-routing.default-policy`.
6. If the resolved policy is `DIRECT`, do not set a proxy.
7. Otherwise set the request proxy to the mihomo supervisor proxy URL for that policy.

Existing per-auth and per-config `proxy-url` behavior remains compatible when Codex routing is disabled. When `mihomo.enabled` and `codex-routing` are configured, the resolved Codex policy is the authoritative proxy decision for Codex requests: `DIRECT` clears proxy use for that request, and non-DIRECT policies use the mihomo supervisor URL. This avoids stale per-auth `proxy_url` metadata overriding the account route map.

## Scheduler Weight

Codex scheduling adds a global repeat count instead of per-account weights.

With accounts `A`, `B`, and `C`, and `codex-scheduler.weight: 2`, the ready-account sequence is:

```text
A, A, B, B, C, C, A, A, B, B, C, C...
```

Rules:

- The default global repeat count is `2`.
- Values lower than `1` are normalized to `1`.
- Existing priority, cooldown, blocked, and disabled states remain in force.
- Accounts removed by quota or authentication health are skipped.
- When a removed account recovers, it re-enters the ready pool and participates in the repeated round-robin sequence again.

## Codex Quota Monitor

CLIProxyAPI adds a Codex quota monitor service.

Monitor behavior:

- Run once during startup.
- Then run every 1 minute.
- Check every known Codex account.
- Query ChatGPT usage through the Codex account credentials.
- Parse 5-hour and weekly or secondary quota windows.
- Mark an account quota-blocked when any critical quota usage is greater than `97%`.
- Mark an account auth-blocked when usage lookup returns `401`.
- Restore the account when later checks succeed and critical quota usage is less than or equal to `97%`.
- Do not remove an account for transient non-401 network or upstream errors. Record the failed check state and keep the previous scheduling state.

The monitor state is backend-owned, not only a frontend display state. Quota-blocked and auth-blocked accounts are removed from scheduler selection until they recover.

## Management Center Simplification

The management center becomes a Codex-only lightweight UI.

Keep:

- Simple password or management-key validation.
- Codex OAuth login.
- Codex authorization URL generation.
- Codex callback URL display and callback handling.
- Codex quota query and display.
- Available model list.

Remove or hide from navigation:

- Non-Codex OAuth providers.
- Non-Codex quota pages and provider-specific widgets.
- Unrelated provider management pages that are not needed for the Codex workflow.

The UI should keep the existing management authentication mechanism and avoid changing public backend route paths unless a Codex-specific endpoint is explicitly added.

## Backend Management API

Existing management endpoints remain compatible.

Codex-focused endpoints should expose:

- Authorization URL generation.
- OAuth callback handling.
- Parsed Codex quota status.
- Available Codex models.
- Codex account status, including email, resolved policy, scheduler state, quota state, auth state, and last check time.

The quota parser should be shared between the backend monitor and management API response so display and scheduling decisions use the same quota interpretation.

## Core Proxy API Compatibility

All core CLIProxyAPI proxy endpoints remain available. The integration changes only these internal behaviors for Codex accounts:

- Account selection.
- Account availability state.
- Per-account route selection.
- Request transport proxy selection.

Clients should not need to change request paths, request bodies, or authentication headers.

## Error Handling

Startup fails when:

- `mihomo.enabled` is true and the mihomo binary cannot be executed.
- The source mihomo config cannot be read or parsed.
- A configured non-DIRECT policy is absent from mihomo config.
- Generated per-policy inbound routing cannot be represented by the local mihomo version.
- A generated local port conflicts with another process.

Runtime behavior:

- If mihomo exits unexpectedly, mark non-DIRECT Codex routes unavailable and continue serving DIRECT routes where possible.
- Log supervisor failures without leaking tokens.
- Keep quota monitor transient errors separate from 401 and high-quota blocking decisions.

## Testing Strategy

Backend tests:

- Config parsing for CSV and structured account routes.
- Email normalization and default-policy fallback.
- Non-DIRECT policy validation against mihomo config data.
- Policy-to-local-proxy URL allocation.
- Scheduler global repeat count sequence.
- Scheduler removal and recovery for quota-blocked and auth-blocked accounts.
- Quota parser coverage for 5-hour, weekly or secondary, code-review, and additional limits.
- Proxy selection precedence for Codex routing.

Frontend tests or checks:

- Login still works with the management key.
- Navigation only exposes Codex-focused pages.
- Codex OAuth URL and callback UI still work.
- Codex quota page renders parsed quota fields.
- Available model list remains accessible.

Integration checks:

- CLIProxyAPI can start mihomo as a child process from the configured binary.
- Generated mihomo config starts successfully.
- Two Codex accounts mapped to different policies receive different local proxy URLs.
- DIRECT account requests bypass mihomo.

Required repository verification after implementation:

```bash
go build -o test-output ./cmd/server && rm test-output
```

## Implementation Order

1. Add config structures and parsing tests.
2. Add account route resolver tests and implementation.
3. Add mihomo supervisor config generation and validation tests.
4. Add scheduler global repeat-count tests and implementation.
5. Add quota parser and monitor tests and implementation.
6. Wire Codex executor proxy selection to route resolver.
7. Add management API status and quota responses.
8. Simplify management center navigation and Codex pages.
9. Run backend build and targeted tests.
10. Run frontend build or available checks.
