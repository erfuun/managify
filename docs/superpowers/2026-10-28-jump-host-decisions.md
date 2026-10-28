# Jump hosts: decisions and deferred minors

Copied from the execution ledger (`.superpowers/sdd/2026-10-28-jump-host/progress.md`, git-ignored) after the final review. Each ruling reads: what was decided, why, and what it costs if wrong.

## Rulings, in the order they were made

- Setup: Ruling: the work is on branch `feat/jump-host` in the main checkout, not a separate worktree — main was clean and nothing else was in flight — cost if wrong: none.
- Task 1: Ruling: `-race` is dropped from the test commands — this Windows toolchain has no cgo — cost if wrong: a data race is caught only by review. The new `Inventory` lock and the pool's `fails`/`via` bookkeeping have never been race-checked.
- Task 2: Ruling: added `TestForwardOffByDefault` — existing test servers must keep rejecting `direct-tcpip` — cost if wrong: none.
- Task 3: Ruling: the timer tests are appended to `dial_test.go`, as the plan says. `TestDialSharedKeySlowPromptBothSucceed` was already flaky before this branch (it failed 1 run in 5 on base 52e9bd3 when run alone, because of its 200 ms handshake timeout under load) and is left as is — cost if wrong: an occasional red suite run.
- Task 4: Ruling: `jump_test.go` is in package `sshx_test` — testkit imports sshx, so an internal test package would be an import cycle — cost if wrong: none.
- Task 4: Ruling: `TestDialJumpSilentTarget` uses a 500 ms timeout and a 5 s limit instead of the plan's 300 ms and 3 s — the jump hop shares the timeout, and 200 ms-class timeouts already flake here — cost if wrong: none.
- Task 4: Ruling: a handshake whose timer fired maps to `UnreachableError{timeout}` through `pausableConn.expired` — otherwise the closed tunnel reads as a raw closed-pipe error — cost if wrong: none.
- Task 6: Ruling: **Jump via** is shown only when there is a server to jump through. The spec has a jumped-through server show a fixed `none`. This avoids a useless field and keeps every existing form test's key sequence valid — cost if wrong: a server others jump through has no Jump via row instead of a fixed `none`.
- Task 6: Ruling: `job.Deps.JumpName` is `func(inventory.Server) string`, not the plan's `func(id string) string`, so `Inv.JumpName` plugs in directly — cost if wrong: none.
- Task 6: Ruling: choices and visibility are tested on the form model (`TestFormJumpChoices`, `TestFormJumpShownForBothAuths`) instead of through rendered output — a stream of frames cannot show what a single row lacks — cost if wrong: none.
- Final: Ruling: hiding Jump via for a jumped-through server stands. The reviewer declined to judge it; nothing could be picked there anyway — cost if wrong: cosmetic.
- Final: Ruling: three rare cases the reviewer declined to judge are left as they are. Each needs a rare race or a hand-edited file and heals on the next probe — cost if wrong: one failed probe.
  - A `Get(web-1)` from a stale snapshot during an edit.
  - Tunnels through a retired entry that are never torn down.
  - The form clearing an invalid jump from a hand-edited yaml.
- Final: Ruling: a guard error from a hand-edited yaml renders as `Jump host <name or ID>: <message>`, and the spec's error table is amended to match. It previously said the bare message. The prefix tells the user which setting is wrong — cost if wrong: wording only.

## Fixed in the final review

- Servers waiting on a jump server's dial each asked its cancelled or failed prompt again. Now a failed dial is shared with the callers waiting on it (`TestCancelledJumpPromptAskedOnce`, RED→GREEN).
- A dead tunnel was handed out right after its jump connection was evicted. Now tunnels close with their jump entry (`TestEvictJumpClosesTunnelAtOnce`, RED→GREEN).
- Servers behind a jump stayed marked "needs action" after the jump was fixed. Now they are retried once the jump answers (`TestFixingJumpRetriesDependants`, RED→GREEN).

## Deferred minors

- ~~`TestDialSharedKeySlowPromptBothSucceed` flakes with its 200 ms handshake timeout under load.~~ Fixed later. The cause was the key's bcrypt KDF (about 95 ms per decrypt) running after the timeout resumed. The decrypt now stays paused (`TestDialKeyDecryptNotTimedOut`).
- The `pausableConn` timer callback does not recheck `paused`/`off` under the lock, and the timer is not stopped on the handshake error path. The race window is tiny.
- `job` evicts the target, which is a no-op, when the AuthError belongs to the jump. It could skip the eviction when the error is a `JumpError`.
- A half-open jump link can hold a target handshake for up to about 60 s, until the jump's keepalive drops it.
- After you save the jump server, a server behind it that was `● up` keeps showing `● up` until the next probe tick, although its tunnel is closed. The next use redials.
