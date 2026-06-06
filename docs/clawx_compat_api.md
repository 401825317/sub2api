# ClawX Compatibility API Draft

Status: draft for `feature/clawx-junfeiai-compat`

This document defines the first Sub2API-side contract for migrating the ClawBox
activation, authorization, built-in provider, and skill-list experience to ClawX.
The ClawX compatibility API is exposed under `/api/clawx/*`. Runtime URLs follow
the public request origin by default, such as `https://junfeiai.com` or
`https://zz-cn.lingzhiwuxian.com`; set `clawx_gateway_base_url` only when ClawX
must be forced to one canonical gateway.

## Goals

- Let a fresh ClawX install activate or log in without manually adding a model
  provider or pasting an API key.
- Reuse existing Sub2API primitives where possible: users, auth tokens, redeem
  codes, API keys, user groups, and the `/v1/*` model gateway.
- Keep model gateway requests on the same public origin as the ClawX bootstrap
  request unless a dedicated `clawx_gateway_base_url` is configured.
- Keep the desktop token/API-key material out of plaintext client config.
- Support short offline grace only after a prior successful authorization check.
- Start with bundled OpenClaw skills for the ClawBox-like skill list; keep remote
  skill marketplace as phase 2.

## Namespace

Primary namespace:

- `GET /api/clawx/bootstrap`
- `POST /api/clawx/activation/check`
- `POST /api/clawx/register`
- `POST /api/clawx/login`
- `POST /api/clawx/auth/verify`
- `POST /api/clawx/auth/unregister-device`
- `POST /api/clawx/relay-token`
- `GET /api/clawx/user/self`

Optional transition alias:

- `/api/clawbox/*` can be mapped to the same handlers only if an old ClawBox
  client must be kept working. New ClawX code should call `/api/clawx/*`.

## Common Headers

Client-to-server headers:

- `Authorization: Bearer <access_token>` for authenticated routes.
- `X-ClawX-Device-Id`: stable client-generated device id. It is an identifier,
  not a secret.
- `X-ClawX-Client-Version`: ClawX app version.
- `X-ClawX-Platform`: `windows`, `macos`, or `linux`.

Server logging must redact `Authorization`, refresh tokens, relay tokens, API
keys, activation tickets, and request bodies containing passwords.

## Response Shape

Handlers should follow the existing Sub2API JSON response helper if possible.
The compatibility payloads below describe the `data` object.

On logical failure, return an application error code that the client can map to
UI states:

- `activation_invalid`
- `activation_expired`
- `activation_consumed`
- `auth_required`
- `device_revoked`
- `entitlement_missing`
- `rate_limited`
- `server_disabled`

## GET /api/clawx/bootstrap

No authentication required.

Purpose:

- Tell ClawX which provider/runtime configuration to install.
- Tell ClawX whether registration, login, activation, skill marketplace, and
  offline grace are enabled.

Response data:

```json
{
  "service": {
    "name": "junfeiai",
    "displayName": "JunFeiAI",
    "apiOrigin": "https://junfeiai.com"
  },
  "auth": {
    "registrationEnabled": true,
    "loginEnabled": true,
    "activationRequired": true
  },
  "runtime": {
    "providerKey": "junfeiai",
    "providerName": "JunFeiAI",
    "baseUrl": "https://junfeiai.com/v1",
    "apiProtocol": "anthropic-messages",
    "defaultModel": "gpt-5.5",
    "fallbackModels": []
  },
  "offline": {
    "graceSeconds": 604800,
    "verifyMemoryCacheSeconds": 300
  },
  "skills": {
    "bundledOpenClawEnabled": true,
    "remoteMarketplaceEnabled": false,
    "remoteMarketplaceBaseUrl": null
  }
}
```

Suggested settings keys:

- `clawx_enabled`
- `clawx_registration_enabled`
- `clawx_login_enabled`
- `clawx_activation_required`
- `clawx_gateway_base_url`
- `clawx_provider_key`
- `clawx_provider_name`
- `clawx_api_protocol`
- `clawx_default_model`
- `clawx_fallback_models`
- `clawx_offline_grace_seconds`
- `clawx_skill_marketplace_enabled`

`clawx_gateway_base_url` has the highest priority. When it is empty, bootstrap
and auth responses derive `service.apiOrigin` and `runtime.baseUrl` from the
current request host, falling back to the public `api_base_url` setting.

## POST /api/clawx/activation/check

No authentication required.

Request:

```json
{
  "code": "XXXX-XXXX",
  "device": {
    "id": "device-uuid",
    "name": "DESKTOP-01",
    "platform": "windows",
    "arch": "x64",
    "appVersion": "0.1.0"
  }
}
```

Response data:

```json
{
  "valid": true,
  "requiresRegistration": true,
  "activationTicket": "short-lived-ticket",
  "expiresIn": 600,
  "entitlementPreview": {
    "groupId": "default",
    "balance": 0,
    "expiresAt": null
  }
}
```

Implementation notes:

- Do not consume the activation code during `check`.
- Prefer a hashed short-lived activation ticket for the following `register`
  call.
- First implementation can reuse redeem-code storage, but a dedicated
  `clawx_activation` code type or activation table is cleaner than overloading
  balance codes.

## POST /api/clawx/register

No authentication required, but may require a valid activation ticket.

Request:

```json
{
  "account": "user@example.com",
  "password": "password",
  "activationTicket": "short-lived-ticket",
  "device": {
    "id": "device-uuid",
    "name": "DESKTOP-01",
    "platform": "windows",
    "arch": "x64",
    "appVersion": "0.1.0"
  }
}
```

Response data:

```json
{
  "accessToken": "jwt",
  "refreshToken": "jwt",
  "expiresIn": 3600,
  "tokenType": "Bearer",
  "user": {
    "id": "user-id",
    "email": "user@example.com",
    "username": "user"
  },
  "device": {
    "id": "device-uuid",
    "status": "active"
  },
  "runtime": {
    "providerKey": "junfeiai",
    "baseUrl": "https://junfeiai.com/v1",
    "apiProtocol": "anthropic-messages",
    "defaultModel": "gpt-5.5"
  }
}
```

Implementation notes:

- Reuse the existing Sub2API user/auth service.
- If ClawX must keep the ClawBox username-only UX, add a username mapping layer.
  The lowest-risk first version is email/password because Sub2API already has
  email-based auth.
- Consume activation only after user creation and device binding succeed.

## POST /api/clawx/login

No authentication required.

Request:

```json
{
  "account": "user@example.com",
  "password": "password",
  "device": {
    "id": "device-uuid",
    "name": "DESKTOP-01",
    "platform": "windows",
    "arch": "x64",
    "appVersion": "0.1.0"
  }
}
```

Response data is the same shape as `register`.

Implementation notes:

- Reuse existing login.
- Create or update the ClawX device record after successful login.

## POST /api/clawx/auth/verify

Authentication required.

Request:

```json
{
  "device": {
    "id": "device-uuid",
    "name": "DESKTOP-01",
    "platform": "windows",
    "arch": "x64",
    "appVersion": "0.1.0"
  },
  "runtime": {
    "providerKey": "junfeiai",
    "defaultModel": "gpt-5.5"
  }
}
```

Response data:

```json
{
  "valid": true,
  "serverTime": "2026-06-05T00:00:00Z",
  "user": {
    "id": "user-id",
    "email": "user@example.com",
    "username": "user"
  },
  "device": {
    "id": "device-uuid",
    "status": "active",
    "lastSeenAt": "2026-06-05T00:00:00Z"
  },
  "entitlements": {
    "providerEnabled": true,
    "modelGatewayEnabled": true,
    "skillsEnabled": true,
    "groupIds": ["default"]
  },
  "runtime": {
    "providerKey": "junfeiai",
    "baseUrl": "https://junfeiai.com/v1",
    "apiProtocol": "anthropic-messages",
    "defaultModel": "gpt-5.5",
    "fallbackModels": []
  },
  "offline": {
    "graceSeconds": 604800,
    "verifyMemoryCacheSeconds": 300
  }
}
```

Offline grace rule:

- Client may continue only when the last successful verify is still inside the
  server-provided `graceSeconds` and the new verify attempt failed because of a
  network/server reachability error.
- Client must not use offline grace when the server returns a definite rejection
  such as `401`, `403`, `device_revoked`, or `entitlement_missing`.

## POST /api/clawx/auth/unregister-device

Authentication required.

Request:

```json
{
  "deviceId": "device-uuid"
}
```

Response data:

```json
{
  "removed": true
}
```

Implementation notes:

- Mark the device revoked instead of hard deleting it.
- Future verify calls for the same device should fail with `device_revoked`.

## POST /api/clawx/relay-token

Authentication required.

Purpose:

- Give ClawX the credential material needed by the built-in JunFeiAI provider.
- First implementation can return or provision a per-device Sub2API API key.
- Later implementation can replace this with a short-lived relay token if a
  relay/proxy layer is added.

Request:

```json
{
  "device": {
    "id": "device-uuid",
    "name": "DESKTOP-01",
    "platform": "windows",
    "appVersion": "0.1.0"
  },
  "scope": ["models:invoke"]
}
```

Response data:

```json
{
  "token": "sk-...",
  "tokenType": "sub2api-api-key",
  "expiresIn": null,
  "runtime": {
    "providerKey": "junfeiai",
    "baseUrl": "https://junfeiai.com/v1",
    "apiProtocol": "anthropic-messages",
    "defaultModel": "gpt-5.5"
  }
}
```

Implementation notes:

- Create one API key per user/device with a predictable internal name such as
  `ClawX <device-id>`.
- Reuse an existing active per-device API key when possible.
- Do not return API keys in list/debug logs.
- Client must store the returned token in OS secure storage.

## GET /api/clawx/user/self

Authentication required.

Response data:

```json
{
  "user": {
    "id": "user-id",
    "email": "user@example.com",
    "username": "user",
    "balance": 0
  },
  "groups": [],
  "devices": []
}
```

## Phase 2 Skill Marketplace

First phase should make ClawX expose bundled OpenClaw skills locally. Remote
marketplace can be added later with:

- `GET /api/clawx/skills/search?q=...`
- `GET /api/clawx/skills/:slug`
- `POST /api/clawx/skills/install-manifest`

The install response should return a signed manifest or package URL, not
arbitrary executable script text.

## Data Model

Suggested new tables:

- `clawx_devices`
  - `id`
  - `user_id`
  - `device_id`
  - `device_name`
  - `platform`
  - `arch`
  - `app_version`
  - `status`
  - `first_seen_at`
  - `last_seen_at`
  - `revoked_at`
- `clawx_activation_tickets`
  - `id`
  - `ticket_hash`
  - `redeem_code_id` or activation-code reference
  - `device_id`
  - `expires_at`
  - `consumed_at`

Existing tables to reuse:

- `users`
- `api_keys`
- `redeem_codes`
- `groups`
- auth refresh-token/session storage

## Implementation Map

- Auth/register/login/refresh: reuse existing Sub2API auth service.
- Activation: reuse redeem-code validation, but do not consume during check.
- Device binding: new ClawX service/table.
- Relay token: reuse `APIKeyService` to create or fetch a per-device key.
- Model gateway: no new gateway path needed; ClawX provider points to
  `https://junfeiai.com/v1`.
- ClawX client provider: built in, hidden from manual provider UI.

## Verification

Backend tests:

- bootstrap returns configured JunFeiAI runtime.
- activation check rejects invalid/expired/consumed codes.
- register consumes activation only after user and device creation succeed.
- login binds device.
- verify rejects revoked devices and missing entitlements.
- relay-token creates or reuses a per-device API key.

Docker full-stack check:

1. Start backend, frontend, PostgreSQL, and Redis from local compose.
2. Create a ClawX activation/redeem code.
3. Call bootstrap, activation/check, register, verify, relay-token.
4. Use returned token against `GET /v1/models`.
5. Revoke the device and confirm verify fails.

## Open Decisions

- Account UX: email/password first, or ClawBox-like username/password.
- Activation storage: dedicated activation code type/table, or reuse redeem
  codes with a strict type marker.
- Relay credential: per-device Sub2API API key first, or build a short-lived
  relay token service now.
- Runtime protocol: confirm whether ClawX should use `anthropic-messages` for
  ClawBox parity or `openai-completions` for broader compatibility.
- Remote skill marketplace: keep disabled for MVP or expose JunFeiAI-backed
  manifest search/install in phase 2.
