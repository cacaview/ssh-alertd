# ssh-alertd

[![CI](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml/badge.svg)](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml)

**English** · [简体中文](docs/README.zh-CN.md) · [繁體中文](docs/README.zh-TW.md) · [日本語](docs/README.ja.md)

A small, modular SSH Alert Daemon written in Go. It watches `sshd` logs and, on
every **successful** SSH login, sends an alert containing:

- Login **IP** (the client's source IP)
- Login **username**
- **Time**
- **Client port** (the client's source port, taken from the sshd log line
  `from <IP> port <port>` — not the server's listening port 22)
- For key logins, the **public-key algorithm** and **SHA256 fingerprint**
  (`ED25519 SHA256:…`), when sshd logs them (see
  [Key fingerprints](#key-fingerprints))

Telegram and SMTP are implemented today. The notifier layer is an interface, so
WhatsApp, WeCom, DingTalk and Feishu can be added as self-contained files
without touching the rest of the system.

## Architecture

```
main.go                       wiring: config -> source -> monitor -> dispatcher
internal/
  event/event.go              LoginEvent domain model
  config/config.go            JSON config + SSH_ALERTD_* env overrides, validation
  monitor/
    source.go                 Source interface; journald & file followers
    monitor.go                sshd line parser -> LoginEvent
  notifier/
    notifier.go               Notifier interface + concurrent Dispatcher
    telegram.go               Telegram Bot backend (one file per backend)
    smtp.go                   SMTP (email) backend
```

Data flow: `Source` (journald/file) → `Monitor` parses "Accepted ..." lines →
`Dispatcher` fans each `LoginEvent` out to all enabled `Notifier`s concurrently.

## Install

### Arch Linux (AUR)

- [`ssh-alertd`](https://aur.archlinux.org/packages/ssh-alertd) — stable release
- [`ssh-alertd-git`](https://aur.archlinux.org/packages/ssh-alertd-git) — latest git

```sh
yay -S ssh-alertd        # or: paru -S ssh-alertd
```

### Debian / Ubuntu

Download the `.deb` for your architecture from the
[latest release](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest):

```sh
sudo apt install ./ssh-alertd_*_amd64.deb
```

### Other Linux

Grab a standalone `linux-amd64` / `linux-arm64` tarball from the
[releases](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest), or
build from source (below).

## Build

```sh
go build -o ssh-alertd .
go test ./...
```

## Releases

Pushing a `v*` tag triggers the [Release workflow](.github/workflows/release.yml),
which builds and attaches to the GitHub Release:

- `.deb` packages for `amd64` and `arm64`
- standalone `linux-amd64` / `linux-arm64` tarballs (binary + unit + example config)
- a `SHA256SUMS` checksum file

```sh
git tag -a v0.1.3 -m "v0.1.3" && git push --follow-tags
```

Keep the version in `packaging/archlinux/PKGBUILD` and
`packaging/debian/changelog` in step with the tag.

## Configure

Copy `config.example.json` to `/etc/ssh-alertd/config.json` and fill in your
Telegram bot token and chat ID.

- `log_source.type`: `journald` (default, reads sshd via `journalctl -f`) or
  `file` (tails `log_source.path` via `tail -F`, e.g. `/var/log/auth.log` on
  Debian/Ubuntu or `/var/log/secure` on RHEL).
- `hostname`: optional override; defaults to the OS hostname.

### Environment variables (optional)

Credentials can live outside `config.json` in `SSH_ALERTD_*` environment
variables. Precedence is **built-in defaults < JSON file < environment**, so
every existing configuration keeps working unchanged.

```sh
sudo install -m0600 ssh-alertd.env.example /etc/ssh-alertd/ssh-alertd.env
sudo systemctl restart ssh-alertd      # the unit reads it via EnvironmentFile=
```

| Variable | Overrides |
| --- | --- |
| `SSH_ALERTD_HOSTNAME` | `hostname` |
| `SSH_ALERTD_LOG_SOURCE_TYPE`, `SSH_ALERTD_LOG_SOURCE_PATH` | `log_source.type` / `.path` |
| `SSH_ALERTD_TELEGRAM_ENABLED`, `_BOT_TOKEN`, `_CHAT_ID`, `_API_BASE`, `_PARSE_MODE` | the matching `telegram.*` keys |
| `SSH_ALERTD_TELEGRAM_MESSAGE_TEMPLATE`, `_MESSAGE_TEMPLATE_FILE` | `telegram.message_template` / `.message_template_file` |
| `SSH_ALERTD_SMTP_ENABLED`, `_HOST`, `_PORT`, `_USERNAME`, `_PASSWORD`, `_FROM`, `_TO`, `_ENCRYPTION` | the matching `smtp.*` keys |
| `SSH_ALERTD_SMTP_SUBJECT_TEMPLATE`, `_BODY_TEMPLATE`, `_BODY_TEMPLATE_FILE`, `_HTML` | the matching `smtp.*` keys |

- The variable names are literally `SSH_ALERTD_` followed by the JSON key in
  upper case (the `SMTP_`/`TELEGRAM_` prefix stands in for the JSON block).
- Booleans accept `true`/`false`/`1`/`0`; `SMTP_PORT` must be an integer.
  Invalid values abort startup with a message naming the variable, and the
  error never echoes the value.
- `SSH_ALERTD_SMTP_TO` is a **semicolon- or space-separated** list. A comma is
  rejected on purpose: it is legal inside a display name such as
  `"Doe, John <j@example.com>"`.
- An empty value still counts as set, so `SSH_ALERTD_SMTP_PASSWORD=` clears a
  password that was in JSON.
- The file is `dotenv`-style (`KEY=VALUE`, `#` comments, optional quotes, no
  shell expansion). It is read from `-env-file`, else `$SSH_ALERTD_ENV_FILE`,
  else `/etc/ssh-alertd/ssh-alertd.env` when that file exists. A file named
  explicitly must exist; a typo is an error rather than a silent fallback.
- The JSON file is always required — environment variables override values,
  they do not replace the file.

### Telegram Bot setup

Three steps: **create the bot to get a token → get the chat_id → fill in
`config.json`**.

**1. Create the bot and get `bot_token`**

1. In Telegram, search for the official **@BotFather** and start a chat.
2. Send `/newbot` and provide a display name and a username (the username must
   end in `bot`, e.g. `my_ssh_alert_bot`).
3. BotFather returns a token like `123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx` —
   that is your `bot_token`.

**2. Get `chat_id`**

A bot cannot message you first; you must message it before it can learn the
chat ID.

- To a private chat: search for your bot → tap **Start** → send any message.
- To a group (for multiple recipients): add the bot to the group and send a
  message there.

Then open this URL in a browser (replace `<TOKEN>` with your token):

```
https://api.telegram.org/bot<TOKEN>/getUpdates
```

Find `"chat":{"id":...}` in the returned JSON. A private chat is a positive
number (e.g. `987654321`); a group is negative (supergroups often start with
`-100`, e.g. `-1001234567890`). If a group returns no updates, disable privacy
mode via `/setprivacy` in BotFather and send the message again.

**3. Fill in `config.json`**

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx",
  "chat_id": "-1001234567890",
  "api_base": "https://api.telegram.org"
}
```

- `enabled` must be `true`, otherwise the backend is not registered (startup
  exits with "no notifiers enabled").
- `bot_token` / `chat_id` are both **strings** — mind the quotes; an empty value
  for either fails startup.
- `api_base` is usually left at the default; change it only when you need a
  reverse proxy (networks where `api.telegram.org` is not reachable directly).

**4. Verify**

Send one message by hand first to confirm the token/chat_id are correct:

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` means the config is correct; you will then receive an alert on the
next real SSH login.

> ⚠️ `config.json` contains the bot token. Set `chmod 600` and keep it out of
> git.

#### Custom message (optional)

By default the message uses a built-in HTML format. To customize the text, set
`message_template` (a [Go template](https://pkg.go.dev/text/template) with the
same event fields as SMTP: `.Username` `.IP` `.Port` `.Method` `.Hostname`
`.Time`):

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExampleBotTokenReplaceMe",
  "chat_id": "-1001234567890",
  "message_template": "🔐 <b>{{.Username}}</b> logged in from <code>{{.IP}}</code> on {{.Hostname}}",
  "parse_mode": "HTML"
}
```

- `message_template`: empty (default) uses the built-in HTML format.
- `message_template_file`: a path read as the message template; it takes
  precedence over `message_template` and is handy for multi-line messages.
- `parse_mode`: `HTML` (default), `MarkdownV2`, `Markdown`, or `none` (plain
  text). With `HTML`, event fields are auto-escaped (via `html/template`) so a
  value like a username can't break the markup; with the other modes you are
  responsible for any escaping the format requires.

> Telegram's "HTML" supports only a small set of inline tags (`<b> <i> <code>
> <pre> <a>` …) — there is no layout or color, so keep templates to formatted
> text.

Ready-to-use examples live in [`examples/telegram/`](examples/telegram/).

### SMTP (email) setup

Add an `smtp` block under `notifiers` to receive alerts by email. This is handy
on networks where Telegram is unreachable, since it talks to your own mail
server directly.

```json
"smtp": {
  "enabled": true,
  "host": "smtp.example.com",
  "port": 587,
  "username": "alert@example.com",
  "password": "your-smtp-password",
  "from": "alert@example.com",
  "to": ["admin@example.com"],
  "encryption": "starttls"
}
```

- `encryption`: `starttls` (default, typically port 587), `tls` (implicit
  TLS / SMTPS, typically port 465), or `none` (port 25, no transport security).
- `port`: optional — defaults to `465` for `tls`, otherwise `587`.
- `username` / `password`: optional; omit `username` to skip authentication
  (e.g. an internal relay). When set, `PLAIN` auth is used (so always over TLS).
- `to`: one or more recipients; at least one is required.
- Multiple notifiers can be enabled at once — each enabled backend gets every
  alert independently.

Telegram and SMTP are independent: enabling one does not require the other.

#### Custom email templates

The subject and body can be customized with [Go templates](https://pkg.go.dev/text/template).
Templates receive the login event with these fields:

| Field | Example |
| --- | --- |
| `.Username` | `alice` |
| `.IP` | `203.0.113.5` |
| `.Port` | `50568` (client source port) |
| `.Method` | `publickey` / `password` |
| `.KeyAlgorithm` | `ED25519`, `RSA`, …; `N/A` when unknown |
| `.KeyFingerprint` | `SHA256:flbXq…`; `N/A` when unknown |
| `.Hostname` | `web-01` |
| `.Time` | a `time.Time`; format with `{{.Time.Format "2006-01-02 15:04:05"}}` |

Telegram `message_template`s receive the same fields, plus the helper methods
`.Algorithm` and `.Fingerprint`, which render `N/A` instead of an empty string.
`.KeyAlgorithm` / `.KeyFingerprint` are filled only for `publickey` logins; the
built-in messages show a `Key:` and `Fingerprint:` line for those and omit them
for password logins.

```json
"smtp": {
  "enabled": true,
  "host": "smtp.example.com",
  "from": "alert@example.com",
  "to": ["admin@example.com"],
  "subject_template": "[ALERT] SSH login {{.Username}}@{{.Hostname}}",
  "body_template": "{{.Username}} logged in from {{.IP}}:{{.Port}} via {{.Method}} at {{.Time.Format \"2006-01-02 15:04:05\"}}",
  "html": false
}
```

- `subject_template` / `body_template`: inline Go templates. Empty values use the
  built-in subject and body.
- `body_template_file`: a path read as the body template; it takes precedence
  over `body_template` and is handy for multi-line or HTML bodies.
- `html: true` renders the body as `text/html` (using `html/template`, which
  auto-escapes the event fields); pair it with a `body_template`.

Templates are compiled at startup, so a malformed template fails fast with a
clear error rather than silently dropping alerts.

Ready-to-use HTML and plain-text examples live in
[`examples/email/`](examples/email/).

### Key fingerprints

For public-key logins, sshd logs the key details on the `Accepted` line:

```
Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 SHA256:VgMvONxNh...
```

ssh-alertd reads the algorithm and fingerprint from that line. It never reads
`authorized_keys` or `known_hosts` and never guesses: when the line has no key
details the alert shows `N/A`, which is how you can tell "key login without
details in the log" apart from a password login.

Whether the details appear depends on sshd's `LogLevel`. If your alerts show
`N/A` for key logins, raise it in `/etc/ssh/sshd_config` and reload sshd:

```ini
LogLevel VERBOSE
```

```sh
sudo sshd -t && sudo systemctl reload sshd
```

This adds the fingerprint (never the key itself or the private key) to the
authentication log, so consider who can read that log.

## Run

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

### As a systemd service

```sh
sudo install -m0755 ssh-alertd /usr/local/bin/ssh-alertd
sudo install -d /etc/ssh-alertd
sudo install -m0640 config.example.json /etc/ssh-alertd/config.json   # then edit
sudo install -m0644 deploy/ssh-alertd.service /etc/systemd/system/ssh-alertd.service

# Create the dedicated ssh-alertd user and give it ownership of the config.
sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
sudo systemd-sysusers
sudo systemd-tmpfiles --create

sudo systemctl daemon-reload
sudo systemctl enable --now ssh-alertd
```

The service runs as the dedicated `ssh-alertd` user, which owns the config and
belongs to `systemd-journal` for the journald source. For the `file` source,
also grant it read access to the auth log (see Privileges below).

## Privileges

ssh-alertd itself does **not require full root**: it does not listen on a port
or write system files — it only **reads sshd login logs** and makes HTTPS
requests to Telegram. The privileges needed depend entirely on how the log is
read, and reading SSH authentication logs is a protected operation on Linux.

| Log source (`log_source.type`) | Reads | Privileges required |
| --- | --- | --- |
| `journald` (default) | systemd journal | root, **or** membership in the `systemd-journal` group |
| `file` | `/var/log/auth.log` (Debian/Ubuntu), `/var/log/secure` (RHEL) | root, **or** a group with read access to the file (usually `adm` on Debian/Ubuntu) |

Confirm access with the matching command first — if you can see output, the
daemon can read it too:

```sh
journalctl -t sshd -t sshd-session -n5    # journald source
tail -n5 /var/log/auth.log                # file source
```

### systemd deployment

The unit runs as the dedicated system user **`ssh-alertd`** (created from
`deploy/ssh-alertd.sysusers` by systemd-sysusers) and is granted read access to
the journal via `SupplementaryGroups=systemd-journal` — so **the journald source
needs no extra authorization when started via systemd**. That user also takes
ownership of `/etc/ssh-alertd/config.json` via `deploy/ssh-alertd.tmpfiles`, so
it can read the token (the file stays `0640`, unreadable to other local users).

> The Arch / Debian packages install these two files and trigger
> sysusers/tmpfiles automatically. For a manual systemd deployment, run once:
>
> ```sh
> sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
> sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
> sudo systemd-sysusers && sudo systemd-tmpfiles --create
> ```

For the **file** source, the `ssh-alertd` user cannot read `auth.log` by
default; add the log file's group to the unit, e.g. on Debian/Ubuntu:

```ini
SupplementaryGroups=systemd-journal adm
```

### Manual debugging

The simplest approach is to run as root directly:

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

Or add your current user to the relevant group to avoid sudo (re-login for the
group to take effect):

```sh
sudo usermod -aG systemd-journal $USER   # journald source
sudo usermod -aG adm $USER               # file source (Debian/Ubuntu)
```

## Troubleshooting

ssh-alertd writes diagnostics to **stderr** (view with
`journalctl -u ssh-alertd -f` under systemd). A healthy start shows
`enabled notifier: telegram` and `monitor started on source ...`; every
successful login produces `detected SSH login: ...` and
`notifier telegram delivered alert ...`.

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Exits at startup with `no notifiers enabled` | `telegram.enabled` is not `true` | Set it to `true` |
| Startup error `telegram enabled but bot_token/chat_id is empty` | Missing credentials | Fill in `bot_token` / `chat_id` (strings) |
| Startup error `parse config ...` | JSON syntax error or unknown field | Config uses `DisallowUnknownFields`; remove typos/extra keys, compare against `config.example.json` |
| **No** `detected` log after a login | Log source can't read sshd | See "No log lines" below |
| `detected` present but `notifier telegram failed` | Telegram delivery failed | See "No Telegram message" below |

**No log lines (no `detected`)**

1. Confirm access first — if you can see output, the daemon can read it (see
   Privileges):
   ```sh
   journalctl -t sshd -t sshd-session -n5    # journald source
   tail -n5 /var/log/auth.log                # file source
   ```
2. Confirm sshd is actually logging logins: `ssh localhost` once and check
   whether `Accepted password/publickey for ...` appears in the command above.
3. For the `file` source, check that `log_source.path` is correct
   (`/var/log/auth.log` on Debian/Ubuntu, `/var/log/secure` on RHEL/CentOS).
4. On a few distros sshd's syslog identifier is neither `sshd` nor
   `sshd-session`, so the journald source filters it out; switch to the `file`
   source.

**No Telegram message (`notifier telegram failed` in the log)**

The error includes the Telegram API response. Common cases:

- `status 401` / `404`: wrong `bot_token` — re-check it with BotFather.
- `status 400 ... chat not found`: wrong `chat_id`, or you never messaged the
  bot — Start/message it, then fetch the id again via `getUpdates`; a group id is
  negative, don't drop the `-100` prefix.
- `status 403 ... bot was blocked`: you blocked the bot — unblock it.
- `send request: ... timeout / no such host`: the server can't reach
  `api.telegram.org` (restricted network) — set `api_base` to your reverse proxy.

A quick curl test separates a "credential problem" from a "program problem":

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` means the credentials are fine; on `"ok":false` the `description`
field explains why.

## Adding a new notifier

1. Create `internal/notifier/<backend>.go` implementing the `Notifier`
   interface (`Name()` and `Send(ctx, event.LoginEvent) error`).
2. Add its config struct to `internal/config/config.go`.
3. Register it in `buildNotifiers` in `main.go`.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the
full text.
