# Managify

A terminal UI for the handful of Linux servers you look after. Save each server once (name, host, user, key or password) and stop retyping usernames, IPs and passwords:

- See every server's status at a glance: up/down, load, RAM and disk.
- Run a command on several servers at once and read each one's output side by side. Every run is also saved to a log file.
- Open an interactive shell with one key.
- Upload a file, optionally running a command right after (for example a deploy script), or download a file.
- Save the commands you repeat as snippets.
- Use sudo without typing the password every time.

Managify runs on Windows, Linux and macOS. The servers must be reachable over SSH; anything that runs `sh` works.

## Install

Needs Go 1.27 or later. From a checkout:

```
go install ./cmd/managify
```

Once the repository is published, `go install github.com/erfuun/managify/cmd/managify@latest` works from anywhere. Then run `managify`.

## Keys

Server list:

| Key | Action |
|-----|--------|
| `↑`/`k`, `↓`/`j` | move |
| `space` | select / unselect a server (actions apply to the selection, else the cursor row) |
| `enter` | open a shell on the cursor row |
| `r` | run a command |
| `u` | upload a file, optionally then run a command |
| `g` | download a file |
| `s` | snippets |
| `a` / `e` / `d` | add / edit / delete a server |
| `R` | refresh status (and ask again for servers that needed a password) |
| `?` | help |
| `q` | quit |

Elsewhere:

| Where | Key | Action |
|-------|-----|--------|
| command input, upload | `ctrl+o` | toggle sudo |
| run view | `tab` / `shift+tab` | next / previous server pane |
| run view | `pgup` / `pgdn` | scroll the pane |
| run view | `ctrl+c` | cancel the run |
| run view, once finished | `ctrl+s` | save what ran as a snippet |
| snippets | `enter` / `n` / `e` / `d` | run / new / edit / delete |
| upload file picker | `enter` / `←`, `backspace` | open a folder or pick a file / go up a folder |
| snippet form | `ctrl+s` | save |
| forms | `tab`, `shift+tab` | next / previous field |
| forms | `space` | toggle a choice |
| everywhere | `esc` | back |

In an upload's remote path, a path ending in `/` means "into that folder", and `~` is your home directory. The path starts filled with the folder you last uploaded to on that server, or `/tmp/` the first time.

## Files

| What | Where |
|------|-------|
| Servers and snippets (`servers.yaml`, `snippets.yaml`) | the OS config dir + `managify`: `%AppData%\managify` on Windows, `~/.config/managify` on Linux, `~/Library/Application Support/managify` on macOS |
| Output logs, one per server per run | `~/Managify/outputs/<server>/` |
| Downloads | `~/Managify/downloads/<server>/` |
| Host keys | `~/.ssh/known_hosts`, shared with OpenSSH |

Managify never overwrites a config file it can't parse. It shows the file and line and exits, so you can fix it by hand.

## Auth and sudo

- **Key servers:** Managify tries the SSH agent first (on Windows, the OpenSSH Authentication Agent service), then the key file. A passphrase is asked once per session.
- **Password servers:** the password is kept in the OS keychain (Windows Credential Manager, macOS Keychain, Secret Service on Linux), or asked once per session if you turn saving off.
- **sudo on a password server** reuses the login password.
- **sudo on a key server** follows the server's "Sudo password" setting:
  - `none`: for passwordless sudo.
  - `saved`: kept in the keychain.
  - `ask`: asked once per session.

  The password is sent only on sudo's standard input.

## Jump hosts

A server that you can reach only through another one, such as a database
server that accepts SSH only from the web server, can jump through it, like
`ssh -J`:

1. Add the web server as usual.
2. Add the database server. Set **Host** to its address as the web server sees
   it, such as `10.0.0.12` or an internal name, and set **Jump via** to the web
   server.

Managify logs in to the web server with that server's own saved settings, then
reaches the database server through it. The jump connection is shared with the
web server's own runs, so you log in to it once.

- There is one hop: a jump server can't jump through another server.
- The jump server's sshd must allow TCP forwarding (`AllowTcpForwarding yes`,
  the default).
- A server that others jump through can't be deleted until they stop using it.
- The list shows `user@host via <jump server>`.

## Security notes

- Passwords and passphrases live only in the OS keychain or in memory. They never appear in the YAML files, logs or error messages.
- Host keys are always checked. A new server's fingerprint is shown and trusted only when you confirm it. A changed host key blocks the connection.
- Output logs hold whatever your commands printed, and that may be sensitive. On Linux and macOS, logs, downloads and config files are created readable by you only (0600, folders 0700). On Windows they inherit your user profile's permissions.
- Cancelling a run is best-effort. Managify sends an interrupt and closes the session, but some servers ignore the interrupt without a terminal, so the remote process may keep running. The pane says so when that can happen.

## Development

```
go vet ./...
go test ./...
```

The tests start an in-process SSH and SFTP server on localhost, so they need no real servers and no Docker. Before a release, run through [docs/manual-test.md](docs/manual-test.md) against one real Linux server.
