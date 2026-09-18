# intproxy

A non-transparent proxy for integrations. Each integration answers on its own
name under `*.<label>.<tld>` — `github.int.<tld>` today — and the credential is
added on the upstream leg, so it never reaches the client.

A client runs:

```
git clone https://github.int.<tld>/<owner>/<repo>.git
```

It runs two ways, and the difference is where the credential and the rules
live. Everything else is shared.

| | broker mode | local mode |
|---|---|---|
| credential | none held; asks a unix socket per request | held here, from a file or the environment |
| policy | none held; the socket's server decides | a static list in this config |
| a rule change | takes effect on the next request | needs a `SIGHUP` |
| a compromise here | lets an attacker mint tokens for attached clients | exposes the tokens themselves |

Broker mode is what a managed fleet uses. Local mode is what makes this thing
standalone: a config file and a token, nothing else.

## Layout

```
internal/credential/   Source, the cache, the broker and static sources
internal/integration/  the Integration interface and the registry
  github/              everything github-shaped
internal/policy/       local-mode rules
internal/intproxy/     bind, tls, host routing, identity, proxying
```

The core knows nothing about github. Adding an integration means implementing
`integration.Integration`, registering it, and adding it to
`internal/integration/all` — no core config fields, no core code.

## Identity

The client is identified by `RemoteAddr`. Nothing else.

In a managed fleet that is trustworthy because the nftables ruleset `dclient`
installs drops any packet from a tap whose source address is not the exact
(tap, address) pair it allocated. Nothing there may weaken that rule.

Standing alone, it is only as good as the network the proxy sits on, and one
address is one client: two people on the same host share whatever credential
that address maps to. There is no way around that within source-IP identity.

## Listening alongside another process

`dproxy` binds `0.0.0.0:443` with `SO_REUSEADDR` and `SO_REUSEPORT`. `intproxy`
binds `10.64.255.254:443` with the same two plus `IP_FREEBIND`.

1. **With `reuseport` on, the listen address must be concrete.** `Validate`
   rejects a wildcard for that reason. A wildcard would collide with `dproxy`
   instead of coexisting with it. Standing alone there is nothing to share the
   port with, so `reuseport` is off and a wildcard is perfectly ordinary.
2. **The two sockets are not a reuseport group.** A group requires identical
   bind addresses, so there is no load balancing between them: the kernel
   scores the specific-address match above the wildcard and every connection to
   `10.64.255.254:443` arrives here. `SO_REUSEPORT` is present only to make the
   second bind legal, and both processes running as root satisfies its same-uid
   requirement.

`IP_FREEBIND` removes any startup ordering: the bind succeeds whether or not
the address exists yet.

## Credentials

`credential.Scope` — integration, client address, credential name, resource,
write — is both what a source is asked for and the cache key. Any field that
changes which token comes back has to be in it.

`credential.Cache` wraps every source: single-flight, renewal
`credential.token_skew` before expiry, and **denials are never cached**, so a
repository attached a second ago works on the very next request. A token with
no expiry is static and never re-fetched.

### Broker mode

`intproxy` asks `dclient` over a unix socket, and `dclient` relays to the
control server with the host's own bearer token. That token is the host's whole
identity — it can create and destroy VMs — so keeping it in `dclient` means a
compromise of `intproxy` buys only the ability to mint repo-scoped tokens for
clients that are already attached.

The pushed config carries no policy at all. Attaching or detaching an
integration therefore takes effect on the next request with no push and no
restart, and a stale policy copy — a detached client that keeps working — is
not possible.

### Local mode

The operator configures tokens per integration, either one or a named map so
each client brings its own. They are read from a file (refused if group or
other can read it) or from the environment, never inline in the config.

An installation token is minted per request and scoped to exactly the repo and
permission the route needs, so github enforces the policy itself. A personal
access token is not: it carries whatever scope it was created with, and this
proxy's routing is the only thing narrowing it. **Prefer fine-grained tokens** —
then the token's scope and the rules below reinforce each other instead of one
carrying all the weight. A client with no `read`/`write` lists forwards
everything and lets github decide, which is the right shape for one.

`auth.kind` accepts `token` today. App authentication is the obvious next
source and slots in behind the same interface.

## Policy (local mode only)

First client whose `source` contains the address wins, so order from most
specific to least. No match is a refusal, logged with the address so the first
"why doesn't this work" takes seconds.

- `read` and `write` are globs over the resource. Write access implies read, so
  a repo listed only under `write` is still clonable.
- Both lists empty forwards everything; the token's own scope is the boundary.
- `unscoped` allows requests that name no resource — graphql, and REST outside
  `/repos/…`. They cannot be authorized per resource, so they get a token that
  covers everything the client may reach. Off unless asked for.
- Matching is case-insensitive, because github treats owner and repo names that
  way and a pattern that worked in only one casing would be a trap.

## Request shapes (github)

| inbound | upstream |
|---|---|
| `GET /{owner}/{repo}[.git]/info/refs?service=git-upload-pack` | `github.com`, read |
| `GET /{owner}/{repo}[.git]/info/refs?service=git-receive-pack` | `github.com`, write |
| `POST /{owner}/{repo}[.git]/git-upload-pack` | `github.com`, read |
| `POST /{owner}/{repo}[.git]/git-receive-pack` | `github.com`, write |
| `/api/v3/repos/{owner}/{repo}/…` | `api.github.com/repos/…`, write on non-GET |
| `/api/v3/…` | `api.github.com/…`, unscoped |
| `/api/graphql`, `/api/v3/graphql` | `api.github.com/graphql`, unscoped |

`gh` treats any host that is not `github.com` as GitHub Enterprise Server, which
is why REST arrives under `/api/v3` and GraphQL at `/api/graphql`. That is the
whole reason `GH_HOST=github.int.<tld>` works with nothing else configured:

```
export GH_HOST=github.int.<tld>
export GH_ENTERPRISE_TOKEN=unused
```

A bare `/repos/…` at the root is deliberately not accepted: `/` is the git
namespace, and `owner=repos` would be ambiguous.

Owner and repo names are validated and **rejected** rather than cleaned. That is
the path-traversal guard.

## Header handling

Every inbound `Authorization`, `Cookie`, `Proxy-Authorization`, `Forwarded`,
`X-Forwarded-*` and `X-Real-Ip` is deleted before our own credential is set. A
client may well have a real `GH_TOKEN`; forwarding it would be a credential
exfiltration path, and honouring it would bypass the authorization model
entirely. Nothing forwarded-ish is sent outbound either — github has no business
learning a client's internal address.

`Git-Protocol` is passed through, because git's v2 protocol negotiation needs it.

On the way back, `Link` and `Location` are rewritten from `api.github.com` and
`github.com` to this host. The `Link` rewrite is mandatory, not cosmetic: `gh`
follows it for pagination, and an unrewritten link points at a host the client
cannot reach and carries no credential, so any paginated call would silently
truncate at page one.

Response **bodies** are not rewritten, so API fields like `clone_url` and
`html_url` still name `github.com`. Known limitation.

## Refusals

Never a 401. `git` reacts to a 401 by prompting for credentials, which is
exactly the confusing outcome this feature exists to avoid. Denials are 403,
rendered three ways so each client shows the reason to the person reading it:
plain text for git, GitHub's `{"message": …}` shape for `gh`, and
`{"errors": […]}` for GraphQL.

That includes a 401 **from upstream**, which is about this proxy's credential
and not the caller's. It is replaced wholesale — headers included, so
`WWW-Authenticate` cannot leak through and cause the same prompt — with a 403
saying whose credential failed. Rare with a minted token; the normal failure
with a personal access token, which expires or gets revoked and cannot renew
itself.

## Timeouts

There is no read or write timeout on the server and no total timeout on the
upstream transport. A clone of a large repository is one long request in each
direction, and any wall-clock deadline would cut it off mid-packfile. Only
header deadlines are set.

A `SIGHUP` reloads the certificate and, in local mode, the policy, so neither a
renewal nor a rule change needs a restart that would kill in-flight clones.
