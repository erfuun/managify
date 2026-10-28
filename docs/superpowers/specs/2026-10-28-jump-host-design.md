# Jump hosts — Design Spec

Date: 2026-10-28
Status: Approved in chat 2026-10-28; spec awaiting review

## Problem

Some servers are reachable only through another one: the DB server accepts SSH
from the web server, not from the laptop. Today Managify can only dial a server
directly, so the DB server cannot be managed at all. OpenSSH solves this with
`ssh -J web db`. This removes the v1 non-goal "jump hosts/bastions".

## Goals

1. A server can name one saved server as its jump host ("Jump via").
2. Everything that works on a direct server works on a jumped one: status
   probe, run, upload/download, shell.
3. The jump hop reuses the jump server's saved login: user, key or password,
   keychain entry, known_hosts check. Nothing is entered twice.
4. Direct servers behave exactly as before.

Success = with `web-1` saved and `db-1` set to jump via `web-1`, the user runs a
command on `db-1` from the list and gets its output, without typing `web-1`'s
details again.

## Non-goals

- Chains of more than one hop. A jump server cannot have a jump of its own.
- Free-text `-J user@host:port` jumps, or reading `ProxyJump` from `~/.ssh/config`.
- `ProxyCommand`, port forwarding, agent forwarding.

## Decisions (from the brainstorm)

| Question | Answer |
|---|---|
| How is the jump named? | Pick a saved server |
| How many hops? | One |
| Deleting a server others jump through? | Blocked, with a message naming them |
| Jump connection | Reuse the pooled connection to the jump server |

## Data

`inventory.Server` gains one field:

```yaml
- id: 9c1e22aa
  name: db-1
  host: 10.0.0.12        # as seen from the jump server
  port: 22
  user: postgres
  auth: key
  jump: 3fa07b10         # ID of web-1; omitted when direct
```

- Stored by **ID**, so renaming `web-1` keeps the link.
- `Host` is resolved on the jump server, so internal names and private IPs work.
- `ConnKey()` includes `Jump`. Changing a server's jump redials it. Editing the
  jump server itself redials the jump connection, which closes every tunnel
  through it; those servers redial on next use (the pool already drops an entry
  whose client's `Wait()` returns).

### Validation (`inventory.validate`, on Add and Update)

- `Jump` must be the ID of an existing server and not the server's own ID.
- The jump server must have no `Jump` of its own.
- A server that any other server jumps through cannot get a `Jump`.

Messages: `jump server not found`, `a server can't jump through itself`,
`web-1 itself jumps through app-1; only one hop is supported`,
`db-1 and db-2 jump through this server, so it can't use a jump itself`.

### Delete (`inventory.Delete`)

Refused while any server jumps through it:
`web-1 is the jump host for db-1, db-2. Change those first.`
The list screen shows this in the footer, like other delete errors.

### Concurrency

Dials run in goroutines while the TUI edits the inventory. `Inventory` gets a
`sync.RWMutex`: `Add`, `Update`, `Delete` lock it; `Get` read-locks it. The
dialer looks up the jump server only through `Get`. The TUI keeps reading
`Inv.Servers` directly; it runs on the same goroutine as every writer.

## Dialing

`sshx.Dialer` gains two hooks:

```go
// Lookup finds a saved server by ID. Jump returns the live connection to a
// jump server. Either nil means jumps are unsupported (a jumped server fails).
Lookup func(id string) (inventory.Server, bool)
Jump   func(ctx context.Context, jump inventory.Server) (Client, error)
```

`main.go` and `testkit` set `Lookup = inv.Get` and `Jump = pool.Get` after
building the pool.

`Client` gains `DialContext(ctx, network, addr string) (net.Conn, error)`;
`*ssh.Client` already has it.

`Dial(ctx, s)` with `s.Jump != ""`:

1. Guard against a hand-edited yaml, which `Load` does not validate. Refuse if
   `s.Jump == s.ID`, `Lookup` misses, or the jump server has a `Jump` itself,
   all before touching the pool. Without this, the pool's per-server locks
   deadlock on a self-jump or a cycle (a→b→a).
2. Get the jump connection through `Jump`. Its errors (unreachable, auth,
   host key, cancelled prompt) come from the jump server's own dial.
3. Open a `direct-tcpip` channel to `s.Addr()` with `DialContext`, bounded by
   the dial timeout.
4. Run the SSH handshake with `s` over that channel, exactly as for a direct
   dial: same auth methods, prompts and host-key check. known_hosts is keyed by
   `s.Addr()` as written (`10.0.0.12`), which is what OpenSSH does for `-J`.
5. Closing the returned client closes the channel only, never the jump
   connection, which the pool owns.

The pool needs no change. `Pool.Get(db)` holds db's lock and then takes the jump
server's lock. Both nest in one direction only, because hops are capped at one
and self-jumps are refused, so they cannot deadlock.

### Handshake timeout through a tunnel

x/crypto's tunnel connection (`chanConn`, v0.58.0) returns an error from
`SetDeadline`, so `pausableConn` would silently do nothing. A target that
accepts TCP but never sends its banner would hang forever. `pausableConn` gets
a timer instead of `SetDeadline`. The timer closes the underlying connection
when it fires, and `pause`/`resume` stop and restart it. This keeps the rule
that a person reading a fingerprint or typing a password is never timed out.
Direct dials keep using the deadline. The timer is used whenever
`SetDeadline` fails.

## Errors

| What failed | Error | Status text |
|---|---|---|
| Jump server unreachable / auth / host key | `*JumpError{Jump: web-1, Err: <its error>}` | `Jump host web-1: <its text>`, with the hint naming web-1 ("Select web-1 and press e…") |
| Jump refuses forwarding (`ssh.Prohibited`) | `*JumpError{…, Err: ErrForwardingDenied}` | `web-1 doesn't allow TCP forwarding (AllowTcpForwarding in its sshd_config).` |
| Target refused / timed out / unknown behind jump | `*UnreachableError{Addr: 10.0.0.12:22, Via: "web-1", Reason}` | `Can't reach 10.0.0.12:22 via web-1 (connection refused)` |
| Jump ID missing, self, or two hops (bad yaml) | `*JumpError{…}` with the validation message | that message |
| User cancels a prompt for the jump | `ErrPromptCancelled`, unwrapped | `Cancelled.` |

`friendly.Short` keeps its words (`auth failed`, `refused`, `timeout`) by
unwrapping `JumpError`. An `AuthError` that belongs to the jump server must not
make `job` forget or evict the target's login. `job`'s evict-on-auth stays a
no-op for the target (failed dials are never pooled).

## TUI

- Server form: a new choice field **Jump via**, after **User**, shown for both
  auth types. It cycles with space and ←/→ like **Sudo password**. The options
  are `none`, then every saved server that is not this one and has no jump of
  its own, by name. A server that others jump through shows only `none`.
- List row: a jumped server's address column reads `10.0.0.12 via web-1`.
- Run log header: `# server: db-1 (postgres@10.0.0.12:22 via web-1)`.
- New-host-key prompt for the target: `New server db-1 (10.0.0.12:22 via web-1)`.

## Testing

`sshtest` gains `Options.Forward`, which accepts `direct-tcpip` channels, dials
the target and copies both ways, and `Options.DenyForward`, which rejects them
with `ssh.Prohibited`.

- sshx: run a command on a target through a jump; the jump connection is shared
  with the jump's own `Pool.Get` (one dial on the jump server); forwarding
  denied; target refuses behind the jump; target accepts TCP but stays silent →
  timeout, not a hang; a prompt open on the target is not timed out; self-jump
  and two-hop yaml return errors without deadlock; target host key stored under
  the target's own `host:port`; closing the target leaves the jump connection open.
- pool: editing the jump server (new `ConnKey`) drops tunnels through it.
- inventory: validate rules (missing, self, two hops both ways), delete blocked
  with names, rename keeps the link, `Jump` round-trips through YAML, `ConnKey`
  changes with `Jump`.
- friendly: each row of the error table.
- tui: form options and cycling, save with a jump, delete blocked footer, list
  row text.

`go vet ./... && go test ./...` passes on Windows; the run is also checked with
`GOOS=linux go vet ./...`.

## Risks

- **A jumped server's status depends on the jump.** If `web-1` is down, `db-1`
  shows `Jump host web-1: …`, not a status of its own. That is correct, but the
  list may look noisy.
- **Shared connection.** A large upload to `db-1` shares bandwidth and the SSH
  window with `web-1`'s own runs. That is acceptable for a personal tool.
- **known_hosts entries for private IPs.** Two jump hosts in different networks
  that both reach a `10.0.0.12` would collide in known_hosts and be reported as
  a changed key. OpenSSH has the same behaviour; the user can set `Host` to a
  distinct name.
