# ADR-0002: Remote deployment on a VPS with OAuth 2.1

## Status
Accepted (2026-10-11). Addresses KI-07, KI-16. The resource-server part (decision items 2,
4, 5) is implemented, see "Implementation notes"; the `mcp-go` v1.x upgrade and the
end-to-end test with Claude and ChatGPT are still open (roadmap Phase 2).

## Context

**Goal:** the owner uses the server from Claude (web, macOS app, iPhone/iPad) and
ChatGPT (web, desktop, mobile) anywhere in the world.

Facts that shape the decision (sources collected 2026-10-10):

| Fact | Source |
|---|---|
| Claude calls custom connectors **from Anthropic's cloud** for every client, mobile included; a connector added on web/desktop shows up on mobile after next login | [support.claude.com 11175166](https://support.claude.com/en/articles/11175166-getting-started-with-custom-connectors-using-remote-mcp), [11176164](https://support.claude.com/en/articles/11176164-use-connectors-to-extend-claude-s-capabilities) |
| Claude auth options for personal plans: OAuth (CIMD, DCR, or "use your own OAuth client" = pre-registered) or none. Static request headers are beta for a limited set of organizations — do not rely on them | [claude.com/docs/connectors/building/authentication](https://claude.com/docs/connectors/building/authentication), [add-unlisted](https://claude.com/docs/connectors/custom/add-unlisted) |
| Claude specifics: sign-in starts only on HTTP **401**; protected-resource `resource` must equal the typed URL including path; 10 s timeout on discovery/registration/token; PKCE S256; only the first authorization server is used; **IPv4 only** (A record required); egress `160.79.104.0/21`; redirect `https://claude.ai/api/mcp/auth_callback` | same |
| Claude limits: tool result ≈150 k characters, 240 s per call; `readOnlyHint` tools can run without confirmation, `destructiveHint` tools always prompt | [building](https://claude.com/docs/connectors/building), [review-criteria](https://claude.com/docs/connectors/building/review-criteria) |
| ChatGPT ("Plugins", formerly connectors/developer mode): custom MCP servers are added on the web; auth = OAuth, none, or mixed; **no static API keys**; read and write tools supported, writes confirmed by default; `search`/`fetch` no longer required; redirect `https://chatgpt.com/connector_platform_oauth_redirect` if the AS supports RFC 9207 `iss`, otherwise a per-plugin URL; egress list at `openai.com/chatgpt-connectors.json` | [developers.openai.com custom-mcp-server](https://developers.openai.com/api/docs/guides/custom-mcp-server), [plugins/build/auth](https://developers.openai.com/plugins/build/auth.md) |
| ChatGPT plan eligibility for **write** tools on Plus/Pro is unclear (help center inaccessible; sources disagree) | open question — verify in the owner's account |
| MCP spec latest is 2026-07-28: RFC 9728 protected-resource metadata MUST, RFC 8414 AS metadata, PKCE S256, RFC 8707 `resource`, audience-bound tokens, CIMD preferred over DCR (DCR deprecated), AS SHOULD return `iss` (RFC 9207) | [modelcontextprotocol.io 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28/changelog) |
| `mcp-go` is at v1.2.1: `WithProtectedResourceMetadata` helper, no token validation or AS; requires Go ≥ 1.25; DNS-rebinding protection (since v0.56) rejects loopback requests with a non-localhost `Host` | [mcp-go releases](https://github.com/mark3labs/mcp-go/releases), PRs #841, #921 |

## Options

| Option | How | Pros | Cons |
|---|---|---|---|
| A. Built-in single-user authorization server | Our binary serves `/.well-known/*`, `/authorize` (passkey or password+TOTP), `/token`; two pre-registered clients (Claude, ChatGPT) | One service; full control; no third party | We own security-critical OAuth code (PKCE, code reuse, refresh rotation, brute force) and its spec drift |
| B. **Pocket ID as AS, our server as resource server** | Pocket ID (passkeys) on `auth.<domain>` issues JWTs with `aud = https://mcp.<domain>/mcp`; our server serves RFC 9728 metadata and validates JWTs via JWKS | Maintained AS with passkeys and UI; CIMD for Claude; our code limited to token validation | One more container; ChatGPT needs a pre-registered client (Pocket ID lacks `private_key_jwt` CIMD); end-to-end compatibility must be tested |
| C. `mcp-auth-proxy` in front of the unchanged server | Proxy does OAuth (password/OIDC) and forwards to the internal server | No code changes | Thin docs; README still claims ChatGPT needs `search`/`fetch`; another component with its own release cadence |
| D. Cloudflare Tunnel + Access Managed OAuth | No open ports; Access acts as AS | No public IP exposure | Vendor dependency; must validate `Cf-Access-Jwt-Assertion`; 125 s proxy read timeout; plain Access (without Managed OAuth) blocks Claude/ChatGPT |
| E. Secret URL (authless + unguessable path/query) | e.g. `/mcp/<random>` | Minutes to set up | Security through obscurity; secrets end up in logs and connector settings; not acceptable beyond a short stopgap |

## Decision

**B**, with **A** as the fallback if B fails end-to-end with either assistant.

1. **Topology:** one VPS, Docker Compose, three containers on an internal network:
   `caddy` (only service publishing 80/443; automatic TLS), `pocket-id`, `things-mcp`.
   Domains `mcp.<domain>` and `auth.<domain>` with A records (IPv4).
2. **Resource server in `things-mcp`:**
   - Upgrade to `mcp-go` v1.x and Go ≥ 1.25 (also update the Dockerfile base image).
   - Serve protected-resource metadata at `/.well-known/oauth-protected-resource/mcp`
     and `/.well-known/oauth-protected-resource` with `resource =
     https://mcp.<domain>/mcp`, `authorization_servers = [https://auth.<domain>]`.
   - Middleware on `/mcp`: no or invalid token → `401` with
     `WWW-Authenticate: Bearer resource_metadata="…", scope="things.read things.write"`; validate `iss`,
     `aud`, `exp`, `nbf`, scope; allowed subject = the owner only.
   - Caddy and the app share a Docker network, so `mcp-go`'s DNS-rebinding protection
     (v0.56+, not in the pinned v0.44) is not triggered; never disable it.
3. **Clients:** Claude via CIMD (allowlist its metadata URL) or a pre-registered public
   client; ChatGPT via a pre-registered confidential client with the exact redirect
   URI shown in its plugin settings.
4. **Scopes:** `things.read` and `things.write`. Write tools require `things.write`;
   granting only `things.read` gives a safe read-only connector (useful on ChatGPT
   plans where write behaviour is unclear, and for Claude Research mode).
5. **REST `/api/*`:** off by default; when enabled, behind the same token validation.
   Static `API_KEY` is removed.
6. **Stopgap:** until step 2 ships, the server is not exposed publicly. If the owner
   needs remote access earlier, option E may be used for at most two weeks with
   access logs disabled for that path and the secret rotated afterwards.
7. **Fallback trigger for A:** if Pocket ID cannot issue audience-bound tokens that both
   Claude and ChatGPT accept, implement A with a vetted library
   (`zitadel/oidc` or `go-oauth2/oauth2`), pre-registered clients only (no DCR), RFC
   9207 `iss`, passkey login via `go-webauthn`.

## Implementation notes (2026-10-11)

Choices the owner approved when KI-07 was implemented:

- **Pocket ID is good enough on paper** (checked 2026-10-11, v2.18): APIs with permissions
  (custom scopes) since v2.10; RFC 9068 JWT access tokens and CIMD since v2.13; RFC 8414
  metadata since v2.14. `aud` is the client id unless the client sends `resource=`, then
  it is the API's resource. Not documented, to verify on the VPS: a resource with a path
  (`https://mcp.<domain>/mcp`), the `typ` header, and what happens when a client asks for
  a permission it was not granted.
- **Strict audience:** a token must carry `aud` = `MCP_PUBLIC_URL` exactly. Tokens whose
  audience is a client id are rejected, so tokens Pocket ID issues for other apps never
  work here.
- **RFC 9068 `typ`:** `at+jwt` or `application/at+jwt` is required, so an ID token is never
  accepted as an access token.
- **Scopes and `ENABLE_WRITES`:** `ENABLE_WRITES` is the server-wide ceiling (write tools
  are not registered without it); `things.write` is per connection. Write tools are hidden
  from, and refused to, tokens without `things.write`; every request needs `things.read`
  (`403 insufficient_scope` otherwise). `scopes_supported` and the challenge advertise
  `things.write` only when writes are enabled.
- **Optional `OAUTH_ALLOWED_CLIENTS`** guards against Pocket ID's "Allow all metadata
  document clients" switch, which grants the API to any CIMD client.
- **No silent open mode:** without OAuth settings the server refuses to start unless
  `INSECURE_NO_AUTH_LOCAL_ONLY=true`, which also binds it to `127.0.0.1` and rejects
  requests whose `Host` or `Origin` is not loopback (DNS rebinding; `mcp-go` v0.44 has no
  such check).
- **Signing keys:** discovered from the issuer's metadata (RFC 8414, then OIDC), cached,
  refreshed hourly or on an unknown `kid` at most once a minute; the last good keys survive
  an outage, and an issuer that was never reachable yields `503`, not `401`.
- **Library:** `go-jose/v4` for JWS/JWT; asymmetric algorithms only.

## Consequences
- Easier: phones and laptops reach the server through the assistants with no Mac, VPN
  or tunnel; credentials stay on one box; read-only connectors are a scope away.
- Harder: three containers to update; an OAuth integration to test on both assistants
  whenever either changes its client; DNS and TLS must stay healthy (monitoring in
  `docs/deployment/vps.md`).
- Security posture: the Things password lives only in the `things-mcp` container's
  secret file; access to tasks requires the owner's passkey; tokens are audience-bound
  and short-lived.
