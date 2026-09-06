# intproxy

A non-transparent proxy for integrations, served under `*.int.<tld>`. Only
`github.int.<tld>` exists today; future integrations become sibling names under
the same label.

A guest runs:

```
git clone https://github.int.<tld>/<owner>/<repo>.git
```

CoreDNS answers `*.int.<tld>` with `10.64.255.254`, an address `dclient` owns on
the host. `intproxy` terminates TLS with the fleet wildcard certificate,
identifies the calling VM by the source address of the connection, obtains a
short-lived GitHub App installation token scoped to that `(vm, repo)` pair, and
reverse-proxies to `github.com` with the credential injected.

The credential never reaches the guest, and the GitHub App private key never
leaves the control server.

## Listening on :443 alongside dproxy

`dproxy` binds `0.0.0.0:443` with `SO_REUSEADDR` and `SO_REUSEPORT`.
`intproxy` binds `10.64.255.254:443` with the same two options plus
`IP_FREEBIND`.

Two invariants that are easy to break by accident:

1. **The listen address must be concrete.** `Config.Validate` rejects a wildcard
   for this reason. A wildcard bind here would collide with `dproxy` instead of
   coexisting with it.
2. **The two sockets are not a reuseport group.** A group requires identical
   bind addresses, so there is no load balancing between them: the kernel scores
   the specific-address match above the wildcard and every connection to
   `10.64.255.254:443` arrives here. `SO_REUSEPORT` is present only to make the
   second bind legal, and both processes running as root satisfies its same-uid
   requirement.

`IP_FREEBIND` removes any startup ordering between `intproxy` and `dclient`:
the bind succeeds whether or not the address exists yet.

## Identity

The VM is identified by `RemoteAddr`. That is trustworthy because the nftables
ruleset `dclient` installs drops any packet from a tap whose source address is
not the exact (tap, address) pair it allocated. Nothing here may weaken that
rule.

## The broker

`intproxy` holds no credential of its own. It asks `dclient` over a unix socket
at `/run/intproxy/broker.sock`, and `dclient` relays to the control server with
the host's own bearer token.

That token is the host's whole identity — it can create and destroy VMs — so
keeping it in `dclient` means a compromise of `intproxy` buys only the ability
to mint repo-scoped tokens for VMs that are already attached.

The control server, not `intproxy`, decides whether a request is allowed. The
pushed config carries no policy at all: no VM-to-integration map. As a result,
attaching or detaching an integration takes effect on the next request with no
push and no restart, and a stale policy copy — a detached VM that keeps working —
is not possible.

Tokens are cached per `(vm, repo, write)` and renewed `broker.token_skew` before
expiry. Denials are never cached, so a repo attached a second ago works on the
next request.

## Request shapes

| inbound | upstream |
|---|---|
| `GET /{owner}/{repo}[.git]/info/refs?service=git-upload-pack` | `github.com`, read |
| `GET /{owner}/{repo}[.git]/info/refs?service=git-receive-pack` | `github.com`, write |
| `POST /{owner}/{repo}[.git]/git-upload-pack` | `github.com`, read |
| `POST /{owner}/{repo}[.git]/git-receive-pack` | `github.com`, write |
| `/api/v3/repos/{owner}/{repo}/…` | `api.github.com/repos/…`, write on non-GET |
| `/api/v3/…` | `api.github.com/…`, integration-scoped |
| `/api/graphql`, `/api/v3/graphql` | `api.github.com/graphql`, integration-scoped |

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

Requests that name no repository — GraphQL, and REST outside `/repos/…` — cannot
be authorized per repository, so they get a token scoped to every repo in the
integrations attached to the calling VM. Still bounded, but coarser.

## Header handling

Every inbound `Authorization`, `Cookie`, `Proxy-Authorization`, `Forwarded`,
`X-Forwarded-*` and `X-Real-Ip` is deleted before our own credential is set. A
guest may well have a real `GH_TOKEN`; forwarding it would be a credential
exfiltration path, and honouring it would bypass the authorization model
entirely. Nothing forwarded-ish is sent outbound either — GitHub has no business
learning a guest's internal address.

`Git-Protocol` is passed through, because git's v2 protocol negotiation needs it.

On the way back, `Link` and `Location` are rewritten from `api.github.com` and
`github.com` to this host. The `Link` rewrite is mandatory, not cosmetic: `gh`
follows it for pagination, and an unrewritten link points at a host the guest
cannot reach and carries no credential, so any paginated call would silently
truncate at page one.

Response **bodies** are not rewritten, so API fields like `clone_url` and
`html_url` still name `github.com`. Known limitation.

## Refusals

Never a 401. `git` reacts to a 401 by prompting for credentials, which is exactly
the confusing outcome this feature exists to avoid. Denials are 403, rendered
three ways so each client shows the reason to the person reading it: plain text
for git, GitHub's `{"message": …}` shape for `gh`, and `{"errors": […]}` for
GraphQL.

## Timeouts

There is no read or write timeout on the server and no total timeout on the
upstream transport. A clone of a large repository is one long request in each
direction, and any wall-clock deadline would cut it off mid-packfile. Only header
deadlines are set.

A `SIGHUP` reloads the certificate in place, so a renewal does not need a restart
that would kill in-flight clones.
