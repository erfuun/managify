# Manual test

Run this against one real Linux server from Windows Terminal before calling a release done. Tick each item only when you see the expected result. Record any failure as an issue, and don't call the release done until it's fixed.

Setup: a Linux server you can reach over SSH, with a key-auth user and a password-auth user. For the sudo items you also need one user with passwordless sudo (`NOPASSWD`) and, if you can, one sudoers entry with `Defaults requiretty`.

## Auth

- [ ] **Key auth via file.** Stop the OpenSSH Authentication Agent service, add a key server with `Key path` set, then confirm the fingerprint. Expected: `● up` with load, RAM and disk.
- [ ] **Key auth via the Windows agent.** Start the agent service and `ssh-add` the key. Clear `Key path` on the server and press `R`. Expected: `● up`, with no passphrase prompt.
- [ ] **Agent stopped falls back.** Stop the agent service again, set `Key path` back, then restart Managify. Expected: `● up`, using the key file and asking for its passphrase if it has one.
- [ ] **Password saved in Credential Manager.** Add a password server with "Save password to keychain" on. Quit and start Managify again. Expected: `● up` with no prompt. Credential Manager lists an entry starting with `managify`.
- [ ] **Password "ask".** Edit the server and turn "Save password to keychain" off. Restart Managify. Expected: one password prompt. After that, `r`, `u` and `enter` don't ask again until you quit.

## Host keys

- [ ] **Changed host key is blocked.** Change the server's line in `~/.ssh/known_hosts` to another key, then press `R`. Expected: `Host key for <name> changed! Connection blocked.`, with the path to known_hosts and the entry to remove. No command runs.

## Run and sudo

- [ ] **Run on two servers.** Select two servers, press `r` and run `uptime`. Expected: one pane per server, each titled `✓ 0`. A log appears in `~/Managify/outputs/<server>/`.
- [ ] **sudo on a password server.** `r`, `ctrl+o`, `whoami`. Expected: `root`, and no password prompt in the pane.
- [ ] **sudo `none` on a NOPASSWD server.** On a key server with "Sudo password" `none`: `r`, `ctrl+o`, `whoami`. Expected: `root`.
- [ ] **sudo on a `requiretty` server.** `r`, `ctrl+o`, `whoami`. Expected: the pane title says `This server requires a tty for sudo. Use the shell.`
- [ ] **ctrl+c cancel message.** Run `sleep 60`, then press `ctrl+c`. Expected: `✗ cancelled locally; process may still be running on <name>`. `esc` returns to the list.

## Shell

- [ ] **Shell and htop resize.** Press `enter` on a server, check that `stty size` matches the window, and run `htop`. Resize the Windows Terminal window. Expected: htop redraws to the new size. Arrow keys and tab completion work. `ctrl+c` stops only the remote command.
- [ ] **First key after the shell.** Type `exit` in the shell. Expected: the list comes back, and the first key you press (for example `j`) registers.

## Files

- [ ] **CRLF upload warning.** Create `x.sh` on Windows with CRLF line endings (`echo hi`). Press `u`, pick it, upload to `/tmp/`, then run `bash /tmp/x.sh`. Expected: `warning: x.sh has Windows line endings (CRLF); shell scripts may fail with $'\r': command not found`, then `uploaded to /tmp/x.sh` and the script's output.
- [ ] **Remote dir remembered.** Press `u` again on the same server. Expected: the remote path starts as `/tmp/`. Upload to `/opt/` (or any writable folder), and the next `u` starts at that folder.
- [ ] **Download.** Press `g` and type `/var/log/syslog` (or any readable file). Expected: `downloaded to ...` and the file in `~/Managify/downloads/<server>/`.

## Snippets

- [ ] **Save and run a snippet.** Run `df -h`, press `ctrl+s`, name it `disk usage`, and save. Expected: `Saved snippet disk usage`. Press `esc`, then `s`, pick it and press `enter`. Expected: the same output. `snippets.yaml` in the config dir holds the entry.

## Files on disk

- [ ] **Everything lands in `~/Managify`.** Expected: `outputs/` and `downloads/` with one folder per server. Logs show the command, exit code and duration, and no passwords. `servers.yaml` and `snippets.yaml` contain no passwords.
