# ssh-alertd

[![CI](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml/badge.svg)](https://github.com/Amekiri-Studio/ssh-alertd/actions/workflows/ci.yml)

[English](../README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · **日本語**

Go で書かれた、小規模でモジュール化された SSH アラートデーモンです。`sshd` のログを監視し、**成功した** SSH ログインのたびに、以下を含むアラートを送信します。

- ログイン元の **IP**（クライアントの送信元 IP）
- ログイン **ユーザー名**
- **時刻**
- **クライアントポート**（クライアントの送信元ポート。sshd のログ行
  `from <IP> port <port>` から取得したもので、サーバーが待ち受けるポート 22 ではありません）
- 鍵ログインでは、sshd が記録している場合に**公開鍵アルゴリズム**と **SHA256 フィンガープリント**
  （`ED25519 SHA256:…`）を含みます（[鍵フィンガープリント](#鍵フィンガープリント)を参照）

現在は Telegram と SMTP が実装されています。通知層はインターフェースになっているため、WhatsApp、
WeCom、DingTalk、Feishu を、システムの他の部分に手を加えることなく
独立したファイルとして追加できます。

## アーキテクチャ

```
main.go                       wiring: config -> source -> monitor -> dispatcher
internal/
  event/event.go              LoginEvent domain model
  config/config.go            JSON 設定 + SSH_ALERTD_* 環境変数による上書き、検証
  monitor/
    source.go                 Source interface; journald & file followers
    monitor.go                sshd line parser -> LoginEvent
  notifier/
    notifier.go               Notifier interface + concurrent Dispatcher
    telegram.go               Telegram Bot backend (one file per backend)
    smtp.go                   SMTP (email) backend
```

データフロー: `Source`（journald/file）→ `Monitor` が "Accepted ..." 行を解析 →
`Dispatcher` が各 `LoginEvent` を、有効なすべての `Notifier` へ並行にファンアウトします。

## インストール

### Arch Linux (AUR)

- [`ssh-alertd`](https://aur.archlinux.org/packages/ssh-alertd) — 安定版リリース
- [`ssh-alertd-git`](https://aur.archlinux.org/packages/ssh-alertd-git) — 最新の git 版

```sh
yay -S ssh-alertd        # or: paru -S ssh-alertd
```

### Debian / Ubuntu

[最新リリース](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest)からアーキテクチャに合った `.deb` をダウンロードします：

```sh
sudo apt install ./ssh-alertd_*_amd64.deb
```

### その他の Linux

[releases](https://github.com/Amekiri-Studio/ssh-alertd/releases/latest) からスタンドアロンの `linux-amd64` / `linux-arm64` tarball を入手するか、ソースからビルドします(下記参照)。

## ビルド

```sh
go build -o ssh-alertd .
go test ./...
```

## リリース

`v*` タグをプッシュすると [リリースワークフロー](../.github/workflows/release.yml) がトリガーされ、
ビルドした成果物が GitHub Release に添付されます。

- `amd64` および `arm64` 向けの `.deb` パッケージ
- スタンドアロンの `linux-amd64` / `linux-arm64` の tarball（バイナリ + unit + サンプル設定）
- `SHA256SUMS` チェックサムファイル

```sh
git tag -a v0.1.3 -m "v0.1.3" && git push --follow-tags
```

`packaging/archlinux/PKGBUILD` と
`packaging/debian/changelog` のバージョンは、タグと揃えておいてください。

## 設定

`config.example.json` を `/etc/ssh-alertd/config.json` にコピーし、
Telegram のボットトークンとチャット ID を記入します。

- `log_source.type`: `journald`（デフォルト。`journalctl -f` 経由で sshd を読み取る）または
  `file`（`tail -F` 経由で `log_source.path` を追従する。例: Debian/Ubuntu では `/var/log/auth.log`、
  RHEL では `/var/log/secure`）。
- `hostname`: 任意の上書き指定。デフォルトは OS のホスト名です。

### 環境変数（任意）

認証情報は `config.json` の外、`SSH_ALERTD_*` 環境変数に置くこともできます。
優先順位は **組み込みの既定値 < JSON ファイル < 環境変数** なので、既存の
設定はそのまま動作します。

```sh
sudo install -m0600 ssh-alertd.env.example /etc/ssh-alertd/ssh-alertd.env
sudo systemctl restart ssh-alertd      # unit が EnvironmentFile= で読み込みます
```

| 変数 | 上書きする項目 |
| --- | --- |
| `SSH_ALERTD_HOSTNAME` | `hostname` |
| `SSH_ALERTD_LOG_SOURCE_TYPE`, `SSH_ALERTD_LOG_SOURCE_PATH` | `log_source.type` / `.path` |
| `SSH_ALERTD_TELEGRAM_ENABLED`, `_BOT_TOKEN`, `_CHAT_ID`, `_API_BASE`, `_PARSE_MODE` | 対応する `telegram.*` |
| `SSH_ALERTD_TELEGRAM_MESSAGE_TEMPLATE`, `_MESSAGE_TEMPLATE_FILE` | `telegram.message_template` / `.message_template_file` |
| `SSH_ALERTD_SMTP_ENABLED`, `_HOST`, `_PORT`, `_USERNAME`, `_PASSWORD`, `_FROM`, `_TO`, `_ENCRYPTION` | 対応する `smtp.*` |
| `SSH_ALERTD_SMTP_SUBJECT_TEMPLATE`, `_BODY_TEMPLATE`, `_BODY_TEMPLATE_FILE`, `_HTML` | 対応する `smtp.*` |
| `SSH_ALERTD_SMTP_RETRY_ATTEMPTS`, `_RETRY_DELAY`, `_RETRY_MAX_DELAY` | 対応する `smtp.*` |

- 変数名は `SSH_ALERTD_` に JSON キーを大文字にしたものを続けた形です（`SMTP_` /
  `TELEGRAM_` は JSON のブロックを表します）。再試行の設定も環境変数で調整でき
  ます（`SMTP_RETRY_DELAY` は `500ms` のような Go の duration）。
- 真偽値は `true`/`false`/`1`/`0`、`SMTP_PORT` は整数です。不正な値では変数名を
  含むメッセージで起動に失敗し、値そのものは表示されません。
- `SSH_ALERTD_SMTP_TO` は**セミコロンまたは空白**区切りのリストです。カンマは
  意図的に拒否します（`"Doe, John <j@example.com>"` のような表示名に使われるため）。
- 空値も「設定済み」として扱うので、`SSH_ALERTD_SMTP_PASSWORD=` とすると JSON
  のパスワードを消去できます。
- ファイルは dotenv 形式です（`KEY=VALUE`、`#` コメント、引用符は任意、シェル
  展開なし）。`-env-file`、`$SSH_ALERTD_ENV_FILE`、存在すれば
  `/etc/ssh-alertd/ssh-alertd.env` の順に読みます。明示したファイルが無い場合は
  エラーです（タイプミスを黙って無視しません）。
- JSON ファイル自体は常に必要です。環境変数は値を上書きするだけで、ファイルを
  置き換えるものではありません。

### Telegram Bot のセットアップ

3 つのステップです: **ボットを作成してトークンを取得 → chat_id を取得 →
`config.json` を記入**。

**1. ボットを作成して `bot_token` を取得する**

1. Telegram で公式の **@BotFather** を検索し、チャットを開始します。
2. `/newbot` を送信し、表示名とユーザー名を指定します（ユーザー名は
   `bot` で終わる必要があります。例: `my_ssh_alert_bot`）。
3. BotFather が `123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx` のようなトークンを返します。
   これがあなたの `bot_token` です。

**2. `chat_id` を取得する**

ボットから先にメッセージを送ることはできません。チャット ID を取得するには、
先にあなたがボットへメッセージを送る必要があります。

- 個人チャットへの場合: ボットを検索 → **Start** をタップ → 任意のメッセージを送信します。
- グループへの場合（複数の宛先向け）: ボットをグループに追加し、そこへメッセージを
  送信します。

その後、ブラウザで次の URL を開きます（`<TOKEN>` をあなたのトークンに置き換えてください）。

```
https://api.telegram.org/bot<TOKEN>/getUpdates
```

返ってきた JSON の中から `"chat":{"id":...}` を探します。個人チャットは正の
数値（例: `987654321`）、グループは負の数値です（スーパーグループはしばしば
`-100` で始まります。例: `-1001234567890`）。グループで更新が返ってこない場合は、
BotFather で `/setprivacy` を使ってプライバシーモードを無効にし、もう一度メッセージを
送信してください。

**3. `config.json` を記入する**

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExxxxxxxxxxxxxxxxxxxxxxxxxx",
  "chat_id": "-1001234567890",
  "api_base": "https://api.telegram.org"
}
```

- `enabled` は `true` でなければなりません。そうでない場合、バックエンドは登録されません
  （起動時に "no notifiers enabled" で終了します）。
- `bot_token` / `chat_id` はどちらも **文字列** です。引用符に注意してください。どちらかが空の値
  だと起動に失敗します。
- `api_base` は通常デフォルトのままにします。変更するのは、リバースプロキシが必要な場合
  （`api.telegram.org` に直接到達できないネットワーク）だけです。

**4. 動作確認**

トークン/chat_id が正しいことを確認するため、まず手動でメッセージを 1 通送信します。

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` であれば設定は正しく、次の実際の SSH ログインでアラートを受信できます。

> ⚠️ `config.json` にはボットトークンが含まれます。`chmod 600` を設定し、
> git の管理対象外にしてください。

#### メッセージのカスタマイズ(任意)

デフォルトでは、メッセージは組み込みの HTML 形式を使います。テキストをカスタマイズするには、`message_template`（SMTP と同じイベントフィールド `.Username` `.IP` `.Port` `.Method` `.Hostname` `.Time` を持つ [Go テンプレート](https://pkg.go.dev/text/template)）を設定します。

```json
"telegram": {
  "enabled": true,
  "bot_token": "123456789:AAExampleBotTokenReplaceMe",
  "chat_id": "-1001234567890",
  "message_template": "🔐 <b>{{.Username}}</b> logged in from <code>{{.IP}}</code> on {{.Hostname}}",
  "parse_mode": "HTML"
}
```

- `message_template`: 空（デフォルト）の場合は組み込みの HTML 形式が使われます。
- `message_template_file`: メッセージテンプレートとして読み込まれるパス。`message_template` より優先され、複数行のメッセージに便利です。
- `parse_mode`: `HTML`（デフォルト）、`MarkdownV2`、`Markdown`、または `none`（プレーンテキスト）。`HTML` の場合、イベントフィールドは自動エスケープされる（`html/template` 経由）ため、ユーザー名のような値がマークアップを壊すことはありません。その他のモードでは、その形式が必要とするエスケープは自分で行う責任があります。

> Telegram の「HTML」は、ごく一部のインラインタグ（`<b> <i> <code> <pre> <a>` など）しかサポートしません。レイアウトや色はないため、テンプレートは書式付きテキストにとどめてください。

すぐに使える例は [`examples/telegram/`](../examples/telegram/) にあります。

### SMTP(メール)のセットアップ

メールでアラートを受信するには、`notifiers` の下に `smtp` ブロックを追加します。
自前のメールサーバーに直接通信するため、Telegram に到達できないネットワークでも便利です。

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

- `encryption`: `starttls`（デフォルト。通常は `port` `587`）、`tls`（暗黙的な
  TLS / SMTPS。通常は `port` `465`）、または `none`（`port` 25。トランスポートの暗号化なし）。
- `port`: 任意。`tls` の場合は `465`、それ以外の場合は `587` がデフォルトです。
- `username` / `password`: 任意。`username` を省略すると認証をスキップします
  （例: 内部リレー）。設定した場合は `PLAIN` 認証が使われます（そのため必ず TLS 上で行ってください）。
- `retry_attempts`: 1 件のアラートあたりの総送信試行回数（初回を含む）。
  `0`/`1`（デフォルト）は 1 回だけ送信します。再試行するのは**接続**、SMTP の
  あいさつ、`DATA` コマンドだけです（そこでの一時的な拒否（4xx）を指数バック
  オフで再試行します）。認証、`MAIL FROM`、`RCPT TO` の拒否は再試行しません。
  また、サーバーが本文を受け取った後の失敗も再試行しません。その時点でアラート
  は既にキューに入っている可能性があるため、重複を避けてエラーとして報告します。
- `retry_delay` / `retry_max_delay`: 最初のバックオフ（デフォルト `1s`、再試行
  ごとに倍）とその上限（デフォルト `5s`）。期間は `"500ms"` / `"2s"` または秒数
  で指定します。ディスパッチャーは送信を 10 秒で打ち切るため、試行とバックオフ
  はその範囲に収めてください（3 回の試行で既定のバックオフを使うと再試行だけで
  最大約 13 秒かかるため、`2` を推奨します）。
- 複数の通知バックエンドを同時に有効化できます。有効な各バックエンドは、すべての
  アラートを独立して受け取ります。

Telegram と SMTP は独立しています。一方を有効にしても、もう一方は必要ありません。

#### メールテンプレートのカスタマイズ

件名と本文は [Go テンプレート](https://pkg.go.dev/text/template)でカスタマイズできます。
テンプレートには、以下のフィールドを持つログインイベントが渡されます。

| フィールド | 例 |
| --- | --- |
| `.Username` | `alice` |
| `.IP` | `203.0.113.5` |
| `.Port` | `50568`（クライアントの送信元ポート） |
| `.Method` | `publickey` / `password` |
| `.KeyAlgorithm` | `ED25519`、`RSA` など。不明な場合は `N/A` |
| `.KeyFingerprint` | `SHA256:flbXq…`。不明な場合は `N/A` |
| `.Hostname` | `web-01` |
| `.Time` | `time.Time`。`{{.Time.Format "2006-01-02 15:04:05"}}` で書式化します |

Telegram の `message_template` でも同じフィールドが使え、さらに `.Algorithm` と
`.Fingerprint` メソッド（空文字の代わりに `N/A` を返す）も利用できます。
`.KeyAlgorithm` / `.KeyFingerprint` は `publickey` ログインでのみ設定され、組み込みの
メッセージはその場合に `Key:` と `Fingerprint:` の行を表示し、パスワードログインでは
表示しません。

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

- `subject_template` / `body_template`: インラインの Go テンプレート。空の値の場合は、組み込みの件名と本文が使われます。
- `body_template_file`: 本文テンプレートとして読み込まれるパス。`body_template` より優先され、複数行や HTML の本文に便利です。
- `html: true` は本文を `text/html` としてレンダリングします（`html/template` を使い、イベントフィールドを自動エスケープします）。`body_template` と組み合わせて使ってください。
- 件名は常に 1 行のヘッダーです。制御文字は除去され空白に畳み込まれるため、
  改行を含む値がメールヘッダー（`Bcc:` など）を追加することはできません。
  非 ASCII の件名は MIME エンコードされ、無効な UTF-8 を含むテンプレート
  ファイルはアラートを失敗させずにサニタイズされます。

テンプレートは起動時にコンパイルされるため、不正なテンプレートはアラートを黙って取りこぼすのではなく、明確なエラーで即座に失敗します。

すぐに使える HTML およびプレーンテキストの例は [`examples/email/`](../examples/email/) にあります。

### 鍵フィンガープリント

公開鍵ログインでは、sshd が `Accepted` 行に鍵の詳細を記録します。

```
Accepted publickey for alice from 203.0.113.5 port 50568 ssh2: ED25519 SHA256:VgMvONxNh...
```

ssh-alertd はこの行からアルゴリズムとフィンガープリントを読み取ります。
`authorized_keys` や `known_hosts` は読まず、推測もしません。行に鍵の詳細が無い
場合、通知には `N/A` が表示されます。これにより「ログに詳細が無い鍵ログイン」と
パスワードログインを区別できます。

詳細が記録されるかどうかは sshd の `LogLevel` に依存します。鍵ログインで `N/A`
と表示される場合は、`/etc/ssh/sshd_config` で次のように上げて sshd を再読み込み
してください。

```ini
LogLevel VERBOSE
```

```sh
sudo sshd -t && sudo systemctl reload sshd
```

これによりフィンガープリント（鍵そのものや秘密鍵ではありません）が認証ログに
記録されるため、そのログを誰が読めるかに注意してください。

## 実行

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

### systemd サービスとして

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

このサービスは専用の `ssh-alertd` ユーザーとして動作します。このユーザーは設定ファイルを所有し、
journald ソースのために `systemd-journal` グループに所属します。`file` ソースの場合は、
さらに認証ログへの読み取りアクセスを付与してください（後述の必要な権限を参照）。

## 必要な権限

ssh-alertd 自体は **完全な root を必要としません**。ポートを待ち受けたりシステムファイルを
書き込んだりはせず、**sshd のログインログを読み取る** ことと、Telegram への HTTPS
リクエストを行うだけです。必要な権限は、ログの読み取り方法に完全に依存します。
そして SSH 認証ログの読み取りは、Linux では保護された操作です。

| ログソース（`log_source.type`） | 読み取り対象 | 必要な権限 |
| --- | --- | --- |
| `journald`（デフォルト） | systemd journal | root、**または** `systemd-journal` グループへの所属 |
| `file` | `/var/log/auth.log`（Debian/Ubuntu）、`/var/log/secure`（RHEL） | root、**または** ファイルへの読み取りアクセスを持つグループ（Debian/Ubuntu では通常 `adm`） |

まず対応するコマンドでアクセスを確認してください。出力が見えれば、デーモンも
同じものを読み取れます。

```sh
journalctl -t sshd -t sshd-session -n5    # journald source
tail -n5 /var/log/auth.log                # file source
```

### systemd でのデプロイ

この unit は専用のシステムユーザー **`ssh-alertd`**（systemd-sysusers により
`deploy/ssh-alertd.sysusers` から作成される）として動作し、
`SupplementaryGroups=systemd-journal` によって journal への読み取りアクセスが付与されます。
したがって **journald ソースは systemd 経由で起動する場合、追加の認可を必要としません**。
このユーザーは `deploy/ssh-alertd.tmpfiles` を介して `/etc/ssh-alertd/config.json` の
所有権も取得するため、トークンを読み取ることができます（ファイルは `0640` のままで、
他のローカルユーザーからは読み取れません）。

> Arch / Debian パッケージはこれら 2 つのファイルをインストールし、
> sysusers/tmpfiles を自動的にトリガーします。手動で systemd デプロイする場合は、
> 一度だけ次を実行してください。
>
> ```sh
> sudo install -Dm644 deploy/ssh-alertd.sysusers /usr/lib/sysusers.d/ssh-alertd.conf
> sudo install -Dm644 deploy/ssh-alertd.tmpfiles /usr/lib/tmpfiles.d/ssh-alertd.conf
> sudo systemd-sysusers && sudo systemd-tmpfiles --create
> ```

**file** ソースの場合、`ssh-alertd` ユーザーはデフォルトでは `auth.log` を読み取れません。
ログファイルのグループを unit に追加してください。例えば Debian/Ubuntu では次のとおりです。

```ini
SupplementaryGroups=systemd-journal adm
```

### 手動デバッグ

最も簡単な方法は、root として直接実行することです。

```sh
sudo ./ssh-alertd -config /etc/ssh-alertd/config.json
```

あるいは、sudo を避けるために現在のユーザーを該当グループに追加します（グループを
反映させるには再ログインが必要です）。

```sh
sudo usermod -aG systemd-journal $USER   # journald source
sudo usermod -aG adm $USER               # file source (Debian/Ubuntu)
```

## トラブルシューティング

ssh-alertd は診断情報を **stderr** に書き込みます（systemd 配下では
`journalctl -u ssh-alertd -f` で確認します）。正常な起動では
`enabled notifier: telegram` と `monitor started on source ...` が表示され、
成功したログインごとに `detected SSH login: ...` と
`notifier telegram delivered alert ...` が出力されます。

| 症状 | 考えられる原因 | 対処 |
| --- | --- | --- |
| 起動時に `no notifiers enabled` で終了する | `telegram.enabled` が `true` でない | `true` に設定する |
| 起動エラー `telegram enabled but bot_token/chat_id is empty` | 認証情報が欠落している | `bot_token` / `chat_id`（文字列）を記入する |
| 起動エラー `parse config ...` | JSON の構文エラー、または不明なフィールド | 設定は `DisallowUnknownFields` を使用します。誤字や余分なキーを削除し、`config.example.json` と比較してください |
| ログイン後に `detected` ログが **出ない** | ログソースが sshd を読み取れない | 後述の「ログ行が出ない」を参照 |
| `detected` は出るが `notifier telegram failed` になる | Telegram への配信に失敗した | 後述の「Telegram メッセージが届かない」を参照 |

**ログ行が出ない（`detected` が出ない）**

1. まずアクセスを確認します。出力が見えれば、デーモンも読み取れます（必要な権限を
   参照）。
   ```sh
   journalctl -t sshd -t sshd-session -n5    # journald source
   tail -n5 /var/log/auth.log                # file source
   ```
2. sshd が実際にログインを記録しているか確認します。`ssh localhost` を一度実行し、
   上記のコマンドで `Accepted password/publickey for ...` が出るかどうかを確認します。
3. `file` ソースの場合、`log_source.path` が正しいか確認します
   （Debian/Ubuntu では `/var/log/auth.log`、RHEL/CentOS では `/var/log/secure`）。
4. 一部のディストリビューションでは sshd の syslog 識別子が `sshd` でも
   `sshd-session` でもないため、journald ソースがそれをフィルターで除外します。`file`
   ソースに切り替えてください。

**Telegram メッセージが届かない（ログに `notifier telegram failed`）**

エラーには Telegram API のレスポンスが含まれます。よくあるケースは次のとおりです。

- `status 401` / `404`: `bot_token` が誤っています。BotFather で再確認してください。
- `status 400 ... chat not found`: `chat_id` が誤っているか、ボットに一度もメッセージを
  送っていません。Start/メッセージ送信を行ってから、`getUpdates` で id を再取得してください。グループ id は
  負の値です。`-100` プレフィックスを省略しないでください。
- `status 403 ... bot was blocked`: ボットをブロックしています。ブロックを解除してください。
- `send request: ... timeout / no such host`: サーバーが `api.telegram.org` に到達できません
  （制限されたネットワーク）。`api_base` をリバースプロキシに設定してください。

簡単な curl テストで「認証情報の問題」と「プログラムの問題」を切り分けられます。

```sh
curl -s "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="ssh-alertd test"
```

`"ok":true` であれば認証情報は問題ありません。`"ok":false` の場合は `description`
フィールドが理由を説明します。

## 新しい通知バックエンドの追加

1. `Notifier` インターフェース（`Name()` と `Send(ctx, event.LoginEvent) error`）を
   実装する `internal/notifier/<backend>.go` を作成します。
2. その設定構造体を `internal/config/config.go` に追加します。
3. `main.go` の `buildNotifiers` に登録します。

## ライセンス

Apache License, Version 2.0 のもとでライセンスされています。全文については
[LICENSE](../LICENSE) を参照してください。
