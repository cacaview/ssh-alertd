# ssh-alertd

[![CI](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml/badge.svg)](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml)

[English](../README.md) · **简体中文** · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md)

一个用 Go 编写的小巧、模块化的 SSH 告警守护进程。它监视 `sshd` 日志，并在每次**成功**的 SSH 登录时发送一条告警，内容包含：

- 登录 **IP**（客户端的来源 IP）
- 登录**用户名**
- **时间**
- **客户端端口**（客户端的来源端口，取自 sshd 日志行 `from <IP> port <port>`——而非服务器的监听端口 22）
- 对密钥登录，在 sshd 记录了这些信息时，包含**公钥算法**与 **SHA256 指纹**
  （`ED25519 SHA256:…`，见下文[密钥指纹](#密钥指纹)）

目前已实现 Telegram 和 SMTP。通知后端层是一个接口，因此 WhatsApp、企业微信、钉钉和飞书都可以作为自包含的文件添加进来，而无需改动系统的其余部分。

## 架构

```
main.go                       wiring: config -> source -> monitor -> dispatcher
internal/
  event/event.go              LoginEvent domain model
  config/config.go            JSON 配置 + SSH_ALERTD_* 环境变量覆盖、校验
  monitor/
    source.go                 Source interface; journald & file followers
    monitor.go                sshd line parser -> LoginEvent
  notifier/
    notifier.go               Notifier interface + concurrent Dispatcher
    telegram.go               Telegram Bot backend (one file per backend)
    smtp.go                   SMTP (email) backend
```

数据流：`Source`（journald/file）→ `Monitor` 解析 "Accepted ..." 行 → `Dispatcher` 将每个 `LoginEvent` 并发地分发给所有已启用的 `Notifier`。

## 安装

### Arch Linux (AUR)

- [`ssh-alertd`](https://aur.archlinux.org/packages/ssh-alertd) —— 稳定版本
- [`ssh-alertd-git`](https://aur.archlinux.org/packages/ssh-alertd-git) —— 最新 git 版本

```sh
yay -S ssh-alertd        # or: paru -S ssh-alertd
```

### Debian / Ubuntu

从[最新发布](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest)下载适用于你的架构的 `.deb`：

```sh
sudo apt install ./ssh-alertd_*_amd64.deb
```

### 其他 Linux

从[发布页面](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest)获取独立的 `linux-amd64` / `linux-arm64` tarball，或从源码构建（见下文）。

## 构建

```sh
go build -o ssh-alertd .
go test ./...
```

## 发布

推送一个 `v*` 标签会触发 [发布工作流](../.github/workflows/release.yml)，它会构建并将以下产物附加到 GitHub Release：

- 面向 `amd64` 和 `arm64` 的 `.deb` 软件包
- 独立的 `linux-amd64` / `linux-arm64` tarball（二进制文件 + unit + 示例配置）
- 一个 `SHA256SUMS` 校验和文件

```sh
git tag -a v0.1.3 -m "v0.1.3" && git push --follow-tags
```

请保持 `packaging/archlinux/PKGBUILD` 和 `packaging/debian/changelog` 中的版本与标签同步。

## 配置

将 `config.example.json` 复制到 `/etc/ssh-alertd/config.json`，并填入你的 Telegram bot token 和 chat ID。

- `log_source.type`：`journald`（默认，通过 `journalctl -f` 读取 sshd）或 `file`（通过 `tail -F` 跟踪 `log_source.path`，例如 Debian/Ubuntu 上的 `/var/log/auth.log` 或 RHEL 上的 `/var/log/secure`）。
- `hostname`：可选的覆盖项；默认使用操作系统主机名。

### 环境变量（可选）

凭据可以放在 `config.json` 之外，通过 `SSH_ALERTD_*` 环境变量提供。优先级为
**内置默认值 < JSON 文件 < 环境变量**，因此现有配置无需任何修改即可继续工作。

```sh
sudo install -m0600 ssh-alertd.env.example /etc/ssh-alertd/ssh-alertd.env
sudo systemctl restart ssh-alertd      # unit 通过 EnvironmentFile= 读取它
```

| 变量 | 覆盖的配置项 |
| --- | --- |
| `SSH_ALERTD_HOSTNAME` | `hostname` |
| `SSH_ALERTD_LOG_SOURCE_TYPE`、`SSH_ALERTD_LOG_SOURCE_PATH` | `log_source.type` / `.path` |
| `SSH_ALERTD_TELEGRAM_ENABLED`、`_BOT_TOKEN`、`_CHAT_ID`、`_API_BASE`、`_PARSE_MODE` | 对应的 `telegram.*` |
| `SSH_ALERTD_TELEGRAM_MESSAGE_TEMPLATE`、`_MESSAGE_TEMPLATE_FILE` | `telegram.message_template` / `.message_template_file` |
| `SSH_ALERTD_SMTP_ENABLED`、`_HOST`、`_PORT`、`_USERNAME`、`_PASSWORD`、`_FROM`、`_TO`、`_ENCRYPTION` | 对应的 `smtp.*` |
| `SSH_ALERTD_SMTP_SUBJECT_TEMPLATE`、`_BODY_TEMPLATE`、`_BODY_TEMPLATE_FILE`、`_HTML` | 对应的 `smtp.*` |
| `SSH_ALERTD_SMTP_RETRY_ATTEMPTS`、`_RETRY_DELAY`、`_RETRY_MAX_DELAY` | 对应的 `smtp.*` |

- 变量名就是 `SSH_ALERTD_` 加上大写的 JSON 键（`SMTP_` / `TELEGRAM_` 前缀代表
  JSON 中的配置块），因此重试参数也可以通过环境变量调整（`SMTP_RETRY_DELAY`
  接受 `500ms` 这类 Go duration）。
- 布尔值接受 `true`/`false`/`1`/`0`；`SMTP_PORT` 必须是整数。非法值会以指明
  变量名的错误中止启动，且错误信息不会回显该值。
- `SSH_ALERTD_SMTP_TO` 是以**分号或空格**分隔的列表。逗号会被有意拒绝：它在
  `"Doe, John <j@example.com>"` 这类显示名中是合法的。
- 空值也算“已设置”，因此 `SSH_ALERTD_SMTP_PASSWORD=` 会清除 JSON 中的密码。
- 文件为 dotenv 格式（`KEY=VALUE`、`#` 注释、可选引号、不做 shell 展开）。
  读取顺序为 `-env-file`、`$SSH_ALERTD_ENV_FILE`、以及存在时的
  `/etc/ssh-alertd/ssh-alertd.env`。显式指定的文件必须存在，拼写错误会报错而
  不是被静默忽略。
- JSON 文件始终必需——环境变量只覆盖取值，不会替代该文件。

### Telegram Bot 配置

三个步骤：**创建 bot 以获取 token → 获取 chat_id → 填入 `config.json`**。

**1. 创建 bot 并获取 `bot_token`**

1. 在 Telegram 中，搜索官方的 **@BotFather** 并开始一个对话。
2. 发送 `/newbot`，并提供一个显示名称和一个用户名（用户名必须以 `bot` 结尾，例如 `my_ssh_alert_bot`）。
3. BotFather 会返回一个形如 `123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx` 的 token——这就是你的 `bot_token`。

**2. 获取 `chat_id`**

bot 无法主动给你发消息；你必须先给它发消息，它才能得知 chat ID。

- 对于私聊：搜索你的 bot → 点击 **Start** → 发送任意一条消息。
- 对于群组（用于多个接收者）：把 bot 加入群组并在群里发一条消息。

然后在浏览器中打开以下 URL（将 `<TOKEN>` 替换为你的 token）：

```
https://api.telegram.org/bot<TOKEN>/getUpdates
```

在返回的 JSON 中找到 `"chat":{"id":...}`。私聊是一个正数（例如 `987654321`）；群组是负数（超级群组通常以 `-100` 开头，例如 `-1001234567890`）。如果某个群组没有返回任何更新，请在 BotFather 中通过 `/setprivacy` 关闭隐私模式，然后重新发送消息。

**3. 填入 `config.json`**

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx",
  "chat_id": "-1001234567890",
  "api_base": "https://api.telegram.org"
}
```

- `enabled` 必须为 `true`，否则该后端不会被注册（启动时会以 "no notifiers enabled" 退出）。
- `bot_token` / `chat_id` 都是**字符串**——注意引号；其中任一为空值都会导致启动失败。
- `api_base` 通常保持默认值；只有当你需要反向代理时（在无法直接访问 `api.telegram.org` 的网络中）才更改它。

**4. 验证**

先手动发送一条消息，以确认 token/chat_id 正确：

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` 表示配置正确；之后你将在下一次真实的 SSH 登录时收到告警。

> ⚠️ `config.json` 包含 bot token。请设置 `chmod 600` 并将其排除在 git 之外。

#### 自定义消息（可选）

默认情况下，消息使用内置的 HTML 格式。如需自定义文本，请设置 `message_template`（一个 [Go 模板](https://pkg.go.dev/text/template)，其事件字段与 SMTP 相同：`.Username` `.IP` `.Port` `.Method` `.Hostname` `.Time`）：

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExampleBotTokenReplaceMe",
  "chat_id": "-1001234567890",
  "message_template": "🔐 <b>{{.Username}}</b> logged in from <code>{{.IP}}</code> on {{.Hostname}}",
  "parse_mode": "HTML"
}
```

- `message_template`：留空（默认）则使用内置的 HTML 格式。
- `message_template_file`：作为消息模板读取的文件路径；它的优先级高于 `message_template`，便于编写多行消息。
- `parse_mode`：`HTML`（默认）、`MarkdownV2`、`Markdown` 或 `none`（纯文本）。在 `HTML` 模式下，事件字段会被自动转义（通过 `html/template`），因此像用户名这样的值不会破坏标记；在其他模式下，则需由你自己负责该格式所要求的任何转义。

> Telegram 的 "HTML" 仅支持一小组内联标签（`<b> <i> <code> <pre> <a>` 等）——没有布局或颜色，因此请将模板限制为带格式的文本。

开箱即用的示例位于 [`examples/telegram/`](../examples/telegram/)。

### SMTP（邮件）配置

在 `notifiers` 下添加一个 `smtp` 块即可通过电子邮件接收告警。这在 Telegram 无法访问的网络中很方便，因为它会直接与你自己的邮件服务器通信。

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

- `encryption`：`starttls`（默认，通常使用 `port` `587`）、`tls`（隐式 TLS / SMTPS，通常使用 `port` `465`）或 `none`（`port` 25，无传输层安全）。
- `port`：可选——对于 `tls` 默认为 `465`，否则为 `587`。
- `username` / `password`：可选；省略 `username` 可跳过认证（例如内部中继）。设置后将使用 `PLAIN` 认证（因此务必通过 TLS）。
- `retry_attempts`：每条告警的总投递次数（含首次）；`0`/`1`（默认）表示只发送
  一次。只有**建立连接**、SMTP 问候和 `DATA` 命令会被重试——这些环节的临时拒绝
  （4xx）会以指数退避重试。认证、发件人（`MAIL FROM`）和收件人（`RCPT TO`）被
  拒绝时从不重试；服务器接收正文之后的任何失败也不重试，因为此时告警可能已经
  入队，程序会报告错误而不是冒重复投递的风险。
- `retry_delay` / `retry_max_delay`：首次退避（默认 `1s`，每次重试翻倍）及其上限
  （默认 `5s`）。时长可写作 `"500ms"` / `"2s"` 或秒数。调度器会在 10 秒后取消
  一次投递，因此尝试次数与退避应控制在该预算内（默认退避下 3 次尝试意味着最多
  约 13 秒的重试时间，建议使用 `2`）。
- 可以同时启用多个通知后端——每个已启用的后端都会独立地收到每一条告警。

Telegram 和 SMTP 相互独立：启用其中一个并不要求启用另一个。

#### 自定义邮件模板

主题和正文可以用 [Go 模板](https://pkg.go.dev/text/template) 自定义。
模板会接收登录事件，包含以下字段：

| 字段 | 示例 |
| --- | --- |
| `.Username` | `alice` |
| `.IP` | `203.0.113.5` |
| `.Port` | `50568`（客户端来源端口） |
| `.Method` | `publickey` / `password` |
| `.KeyAlgorithm` | `ED25519`、`RSA` 等；未知时为 `N/A` |
| `.KeyFingerprint` | `SHA256:flbXq…`；未知时为 `N/A` |
| `.Hostname` | `web-01` |
| `.Time` | 一个 `time.Time`；用 `{{.Time.Format "2006-01-02 15:04:05"}}` 格式化 |

Telegram 的 `message_template` 同样可以使用这些字段，另外还有 `.Algorithm` 与
`.Fingerprint` 方法（缺省时输出 `N/A` 而不是空字符串）。`.KeyAlgorithm` /
`.KeyFingerprint` 仅在 `publickey` 登录时填写；内置消息在此时显示 `Key:` 与
`Fingerprint:` 两行，密码登录时不显示。

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

- `subject_template` / `body_template`：内联的 Go 模板。留空则使用内置的主题和正文。
- `body_template_file`：作为正文模板读取的文件路径；它的优先级高于 `body_template`，便于编写多行或 HTML 正文。
- `html: true` 会将正文渲染为 `text/html`（使用 `html/template`，它会自动转义事件字段）；请与 `body_template` 搭配使用。
- 主题始终是单行邮件头：控制字符会被去除并折叠为空格，因此含有换行的值无法
  添加邮件头（例如 `Bcc:`）。非 ASCII 主题会进行 MIME 编码；包含非法 UTF-8 的
  模板文件会被清洗，而不是导致告警发送失败。

模板在启动时编译，因此格式错误的模板会立即以清晰的错误信息失败，而不会悄无声息地丢弃告警。

开箱即用的 HTML 和纯文本示例位于 [`examples/email/`](../examples/email/)。

### 密钥指纹

对于公钥登录，sshd 会在 `Accepted` 行中记录密钥信息：

```
Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 SHA256:VgMvONxNh...
```

ssh-alertd 只从这一行读取算法与指纹，不会读取 `authorized_keys` 或 `known_hosts`，
也不会猜测：当日志行中没有密钥信息时，告警显示 `N/A`，据此可以区分“日志中没有
详细信息的密钥登录”和密码登录。

是否记录这些信息取决于 sshd 的 `LogLevel`。如果密钥登录的告警显示 `N/A`，请在
`/etc/ssh/sshd_config` 中提高日志级别并重新加载 sshd：

```ini
LogLevel VERBOSE
```

```sh
sudo sshd -t && sudo systemctl reload sshd
```

这会把指纹（不是公钥本身，更不是私钥）写入认证日志，请留意哪些人能够读取该日志。

## 运行

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

### 作为 systemd 服务

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

该服务以专用的 `ssh-alertd` 用户运行，该用户拥有配置文件，并隶属于 `systemd-journal` 组以使用 journald 源。对于 `file` 源，还需授予它对认证日志的读取权限（见下文权限要求）。

## 权限要求

ssh-alertd 本身**不需要完整的 root 权限**：它不监听端口，也不写入系统文件——它只**读取 sshd 登录日志**并向 Telegram 发起 HTTPS 请求。所需的权限完全取决于日志的读取方式，而在 Linux 上读取 SSH 认证日志是一项受保护的操作。

| 日志源 (`log_source.type`) | 读取内容 | 所需权限 |
| --- | --- | --- |
| `journald`（默认） | systemd journal | root，**或**加入 `systemd-journal` 组 |
| `file` | `/var/log/auth.log`（Debian/Ubuntu）、`/var/log/secure`（RHEL） | root，**或**一个对该文件有读取权限的组（在 Debian/Ubuntu 上通常是 `adm`） |

请先用对应的命令确认访问权限——如果你能看到输出，守护进程也能读取它：

```sh
journalctl -t sshd -t sshd-session -n5    # journald source
tail -n5 /var/log/auth.log                # file source
```

### systemd 部署

该 unit 以专用系统用户 **`ssh-alertd`**（由 systemd-sysusers 从 `deploy/ssh-alertd.sysusers` 创建）运行，并通过 `SupplementaryGroups=systemd-journal` 被授予对 journal 的读取权限——因此**通过 systemd 启动时，journald 源无需任何额外授权**。该用户还通过 `deploy/ssh-alertd.tmpfiles` 取得 `/etc/ssh-alertd/config.json` 的所有权，从而能够读取 token（文件保持 `0640`，对其他本地用户不可读）。

> Arch / Debian 软件包会安装这两个文件并自动触发 sysusers/tmpfiles。对于手动的 systemd 部署，运行一次：
>
> ```sh
> sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
> sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
> sudo systemd-sysusers && sudo systemd-tmpfiles --create
> ```

对于 **file** 源，`ssh-alertd` 用户默认无法读取 `auth.log`；将该日志文件所属的组添加到 unit 中，例如在 Debian/Ubuntu 上：

```ini
SupplementaryGroups=systemd-journal adm
```

### 手动调试

最简单的方式是直接以 root 运行：

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

或者将你当前的用户加入相关的组以避免使用 sudo（需重新登录以使组生效）：

```sh
sudo usermod -aG systemd-journal $USER   # journald source
sudo usermod -aG adm $USER               # file source (Debian/Ubuntu)
```

## 故障排查

ssh-alertd 将诊断信息写入 **stderr**（在 systemd 下用 `journalctl -u ssh-alertd -f` 查看）。健康的启动会显示 `enabled notifier: telegram` 和 `monitor started on source ...`；每次成功登录都会产生 `detected SSH login: ...` 和 `notifier telegram delivered alert ...`。

| 症状 | 可能原因 | 解决方法 |
| --- | --- | --- |
| 启动时以 `no notifiers enabled` 退出 | `telegram.enabled` 不为 `true` | 将其设置为 `true` |
| 启动错误 `telegram enabled but bot_token/chat_id is empty` | 缺少凭据 | 填入 `bot_token` / `chat_id`（字符串） |
| 启动错误 `parse config ...` | JSON 语法错误或存在未知字段 | 配置使用 `DisallowUnknownFields`；移除拼写错误/多余的键，并对照 `config.example.json` |
| 登录后**没有** `detected` 日志 | 日志源无法读取 sshd | 见下文"无日志行" |
| 出现 `detected` 但 `notifier telegram failed` | Telegram 投递失败 | 见下文"收不到 Telegram 消息" |

**无日志行（没有 `detected`）**

1. 先确认访问权限——如果你能看到输出，守护进程就能读取它（见权限要求）：
   ```sh
   journalctl -t sshd -t sshd-session -n5    # journald source
   tail -n5 /var/log/auth.log                # file source
   ```
2. 确认 sshd 确实在记录登录：执行一次 `ssh localhost`，并在上面的命令中检查是否出现 `Accepted password/publickey for ...`。
3. 对于 `file` 源，检查 `log_source.path` 是否正确（Debian/Ubuntu 上为 `/var/log/auth.log`，RHEL/CentOS 上为 `/var/log/secure`）。
4. 在少数发行版上，sshd 的 syslog 标识符既不是 `sshd` 也不是 `sshd-session`，因此 journald 源会将其过滤掉；这时改用 `file` 源。

**收不到 Telegram 消息（日志中出现 `notifier telegram failed`）**

错误信息中包含 Telegram API 的响应。常见情况：

- `status 401` / `404`：`bot_token` 错误——用 BotFather 重新核对。
- `status 400 ... chat not found`：`chat_id` 错误，或者你从未给 bot 发过消息——先 Start/给它发消息，然后通过 `getUpdates` 重新获取 id；群组 id 是负数，不要丢掉 `-100` 前缀。
- `status 403 ... bot was blocked`：你屏蔽了该 bot——取消屏蔽。
- `send request: ... timeout / no such host`：服务器无法连接 `api.telegram.org`（受限网络）——将 `api_base` 设置为你的反向代理。

一个快速的 curl 测试可以把"凭据问题"和"程序问题"区分开：

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` 表示凭据没问题；当返回 `"ok":false` 时，`description` 字段会说明原因。

## 添加新的通知后端

1. 创建 `internal/notifier/<backend>.go`，实现 `Notifier` 接口（`Name()` 和 `Send(ctx, event.LoginEvent) error`）。
2. 将其配置结构体添加到 `internal/config/config.go`。
3. 在 `main.go` 的 `buildNotifiers` 中注册它。

## 许可证

基于 Apache License, Version 2.0 授权。完整文本见 [LICENSE](../LICENSE)。
