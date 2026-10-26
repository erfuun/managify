# Managify — Design Spec

Date: 2026-10-27
Status: Approved 2026-10-27

## Problem

Working with a handful of personal Linux servers means repeatedly looking up and
typing the IP, username and password, then juggling `ssh`, `scp` and copy-paste to
upload a file, run something, and get the output back. Managify removes that
friction: enter a server's details once, then connect, run, upload and download by
picking it from a list.

## Goals (v1)

1. **No retyping credentials.** A server is defined once in the TUI. Every later
   action uses the saved profile and keychain.
2. **Upload → run → output back in one flow**, across one or many servers, with
   output both shown live and saved to local log files.
3. **Reusable actions.** Frequent commands and upload+run recipes are saved as
   snippets and run with a few keys.

Success = from the server list, the user can open a shell, run a command on several
servers, or upload-and-run a script without typing a host, user or password.

## Non-goals (v1)

- Windows/BSD *target* servers (targets are Linux; status probe reads `/proc`).
- Importing `~/.ssh/config`, jump hosts/bastions, port forwarding.
- Team sharing, multi-user inventory, audit logs.
- A non-interactive CLI mode.
- Commands that need interactive input other than a sudo password (see
  [sudo](#sudo)). Run mode has no PTY. Use the shell for those.
- Directory upload/download (single files only).
- Scheduled/recurring tasks.

## Platform & stack

- Managify runs on Windows (primary), macOS and Linux. Single static binary.
- Go 1.27 (installed toolchain). Module path: `managify` (works locally; rename
  to `github.com/<user>/managify` if/when it's pushed to GitHub).
- Libraries:
  - `github.com/charmbracelet/bubbletea`, `bubbles`, `lipgloss`: TUI
  - `golang.org/x/crypto/ssh`, `.../ssh/knownhosts`, `.../ssh/agent`: SSH
  - `github.com/pkg/sftp`: file transfer
  - `github.com/zalando/go-keyring`: OS keychain
  - `golang.org/x/term`: raw mode / terminal size for the shell
  - `github.com/Microsoft/go-winio`: Windows OpenSSH agent named pipe
  - `gopkg.in/yaml.v3`: config files
  - `github.com/charmbracelet/x/exp/teatest`: TUI tests

## Architecture

```
managify/
  cmd/managify/main.go     wire dependencies, start TUI
  internal/inventory/      server profiles: load/save servers.yaml, CRUD
  internal/secrets/        Store interface; keyring impl + in-memory fake
  internal/sshx/           dialing, auth, host-key checks, per-server client pool
  internal/remote/         Run (streaming), Probe (status), Upload, Download
  internal/outputs/        per-server, per-run output log files
  internal/snippets/       saved commands / upload+run recipes
  internal/tui/            Bubble Tea models for every screen
```

Rules:
- Only `tui` imports Bubble Tea. All other packages are plain Go, testable headless.
- `remote` depends on a `sshx.Client` interface (wrapping `*ssh.Client`), never on
  dialing directly.
- Multi-server operations run one goroutine per server. Results reach the TUI as
  `tea.Msg` values tagged with the server ID. No global concurrency cap (a handful
  of servers).
- Secrets never touch disk.

### Package contracts

- **inventory**: `Load(path) (*Inventory, error)`, `Add/Update/Delete(Server)`,
  `Save()`. Atomic save (write temp file + rename).
- **secrets**: `type Store interface { Get(id) (string, error); Set(id, pw) error; Delete(id) error }`.
  Keyring service name `managify`, account = server ID. Also an in-memory
  session cache for "ask each session" passwords and key passphrases.
- **sshx**: `Pool.Get(ctx, Server) (Client, error)` returns a cached live client or
  dials. A failed client is evicted and redialed on next `Get`. Dial timeout 10 s.
  Keepalive every 30 s. Auth chain:
  - `auth: key`: ssh-agent if reachable (`SSH_AUTH_SOCK` on Unix,
    `\\.\pipe\openssh-ssh-agent` on Windows), then `key_path`. An encrypted key
    prompts for its passphrase (cached in memory for the session).
  - `auth: password`: keychain if `save_password: true`, else session cache, else
    prompt via a callback supplied by the TUI. Also offers keyboard-interactive
    answering with the same password.
  - Prompts (passwords, passphrases, host-key confirmations) are serialized: when
    several servers dial at once, the TUI shows them one at a time, each labelled
    with the server name. Cancelling a prompt fails only that server.
- **remote**:
  - `Run(ctx, Client, cmd, sink func(Line))` → `Result{ExitCode, Duration, Err}`.
    `Line{Stream: Stdout|Stderr, Text}`. Cancel via ctx is **best-effort**: it
    sends SIGINT, then closes the session. Without a PTY some servers ignore the
    signal, so the remote process may keep running. The TUI says so ("cancelled
    locally; process may still be running on web-1").
  - `RunOpts{Sudo bool, SudoPassword string}`: see [sudo](#sudo).
  - `Probe(ctx, Client) (Status, error)` runs one command:
    `cat /proc/loadavg; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo; df -P /`
    and parses load (1 min), RAM used %, root disk used %.
  - `Upload(ctx, Client, localPath, remotePath, progress func(n, total))`,
    `Download(...)` symmetric. Remote path ending in `/` means "into this directory,
    same file name". Overwrites existing remote files.
- **outputs**: `Begin(serverName, cmd) (*Log, error)` → writer. Log file
  `<data>/outputs/<server-name>/<YYYYMMDD-HHMMSS>.log`. Header (server, host,
  command, start time), lines (stderr prefixed `[err] `), footer (exit code,
  duration).
- **snippets**: `Load/Save`, `Snippet{Name, Upload *{Local, Remote}, Command, Sudo}`.

### sudo

Run mode can execute a command with sudo and answer the password prompt itself.

- Command input has a `[ ] sudo` toggle (`ctrl+o`). Snippets carry `sudo: true`.
- The password comes from the server's login password (`auth: password`), or for
  key-auth servers from an optional **sudo password** field in the add/edit form
  (keychain account `<id>:sudo`, same save/ask-each-session choice).
- Remote command:
  `sudo -S -p '' -- sh -c 'exec </dev/null; <cmd>'`, with `<cmd>` single-quote
  escaped. Managify writes the password plus a newline to stdin, then closes stdin.
  `exec </dev/null` means the user's command can never read the password, even when
  sudo doesn't consume it (NOPASSWD or cached credentials).
- If sudo reports an incorrect password, the pane shows "sudo password rejected.
  Press e to update it" and the password is never echoed.
- `sudo_password` modes: `saved` (keychain), `ask` (prompt once per session, same
  queued prompt as above), `none` (server has NOPASSWD sudo: run
  `sudo -n -- sh -c 'exec </dev/null; <cmd>'`, no password written; `-n` fails
  fast instead of hanging). Password-auth servers default to their login password.

## Data files

Config dir = `os.UserConfigDir()/managify` (Windows: `%AppData%\managify`).
Data dir = `~/Managify` (easy to find; holds `outputs/` and `downloads/`).
Files and dirs are created with user-only permissions (0600/0700 on Unix; default
user-profile ACLs on Windows).

`servers.yaml`:
```yaml
servers:
  - id: 7f3a9c21          # random, stable; keychain account
    name: web-1
    host: 203.0.113.10
    port: 22
    user: root
    auth: key             # key | password
    key_path: ~/.ssh/id_ed25519
  - id: b81e04d7
    name: db-1
    host: 203.0.113.20
    port: 22
    user: admin
    auth: password
    save_password: true   # false = ask once per session
    last_remote_dir: /tmp/
  # key-auth servers may add: sudo_password: saved | ask | none
```

`snippets.yaml`:
```yaml
snippets:
  - name: deploy
    upload: { local: C:\work\deploy.sh, remote: /tmp/deploy.sh }
    command: bash /tmp/deploy.sh
    sudo: true
  - name: disk usage
    command: df -h
```

Host keys: `~/.ssh/known_hosts` (created if missing), via `knownhosts`.

## Screens & flow

**Server list (home)**
```
 Managify                                   [a]dd [e]dit [d]el  [?]help
 ─────────────────────────────────────────────────────────────────────
 [x] web-1    root@203.0.113.10   ● up   load 0.4  RAM 41%  disk 63%
 [ ] db-1     admin@203.0.113.20  ● up   load 1.9  RAM 78%  disk 81%
 [x] backup   erfan@10.0.0.5      ○ down (timeout)
 ─────────────────────────────────────────────────────────────────────
 space select · enter shell · r run · u upload · g download · s snippets
```
- Status probes every 30 s in the background, `R` refreshes now. Slow servers never
  block the UI.
- `space` toggles selection. Actions target the selection, or the cursor row if
  nothing is selected.
- `q` / `ctrl+c` quits from home.

**Add/edit form (`a` / `e`)**: name, host, port (22), user, auth (key | password),
key path (+ optional sudo password), or password + "save to keychain" toggle. On
save: test connection.
First contact shows the host-key fingerprint (SHA256) for confirmation and appends
it to `known_hosts`. Delete (`d`) asks for confirmation and removes the keychain
entry too.

**Run (`r`)**: command input → run view. One pane per server for up to 3 servers,
tabs (`tab`/`shift+tab`) beyond that. Live streaming, stderr in red, pane title
shows `✓ 0` / `✗ <code>` when done. `ctrl+o` in the command input toggles sudo.
`ctrl+c` cancels on all servers (best-effort, see `remote.Run`). `ctrl+s` saves
the command as a snippet. `esc` returns home.

**Upload (`u`)**: local file picker → remote path (prefilled from
`last_remote_dir`) → optional "then run" command → progress per server → run view
if a command was given.

**Download (`g`)**: remote path → saves to `~/Managify/downloads/<server-name>/`.

**Shell (`enter`)**: TUI suspends via `tea.Exec`. Interactive session with PTY
(`xterm-256color`), stdin in raw mode. Window size is synced via SIGWINCH on Unix
and polled every 250 ms on Windows. `exit` returns to the list.

**Snippets (`s`)**: list. `enter` runs on the target servers (upload first if
defined), `n` new, `e` edit, `d` delete.

## Error handling

- Failures are per server: one failing server shows its error in its own row or pane.
  Others continue.
- Errors are translated to plain words with a next step, e.g. password rejected →
  "Password rejected. Press e to update it". Unreachable → "Can't reach
  203.0.113.10:22 (timeout)".
- Host key **mismatch** blocks the connection with a clear warning and the
  `known_hosts` path. No in-app override.
- Dropped connections are evicted from the pool and redialed on next action.
- Commands have no timeout. User cancels with `ctrl+c`.
- Config file parse errors are shown on startup with file and line. The app does not
  overwrite a file it couldn't parse.

## Security

- Passwords and key passphrases are held only in the OS keychain or process memory.
  They never appear in YAML, logs, output files or error messages.
- Host keys are always verified (`knownhosts`). TOFU only with explicit confirmation.
- Output logs may contain sensitive command output. They are stored with
  user-only permissions.

## Testing

- **Unit**: inventory, snippets, outputs (temp dirs); probe output parsing; secrets
  via in-memory fake.
- **Integration**: an in-process SSH + SFTP test server (`x/crypto/ssh` +
  `pkg/sftp` server) on a random localhost port. It covers key auth, password auth,
  wrong password, host-key mismatch, streaming stdout/stderr, exit codes,
  cancellation, upload/download. The sudo wrapper is tested with unit tests on the
  generated command string and quoting. Real sudo behaviour is checked in the manual
  test. No Docker, no real servers.
- **TUI**: `teatest` for add server, multi-select, run + cancel, upload flow.
- **Manual**: one real Linux server before v1 is called done (Windows host:
  shell resize, agent named pipe, keychain).

## Build order (each milestone usable)

1. Inventory + add/edit form + server list (no connections yet)
2. sshx pool + status probes
3. Run on many + output logs + sudo
4. Interactive shell
5. Upload/download + upload-then-run
6. Snippets

## Risks

- **Windows shell handoff**: raw mode and resize behaviour in Windows Terminal vs
  legacy console. Verify early in milestone 4.
- **Windows ssh-agent**: needs the OpenSSH Authentication Agent service running.
  If it's unreachable, fall back silently to `key_path`.
- **sudo `requiretty`**: some sudoers configs refuse sudo without a tty. Detect
  the "you must have a tty" message and show "this server requires a tty for sudo.
  Use the shell". Covered in the manual test.
