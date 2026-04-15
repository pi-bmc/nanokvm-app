# NanoKVM サーバー

これは NanoKVM のバックエンドサーバーの実装です。

詳細なドキュメントについては、[Wiki](https://wiki.sipeed.com/nanokvm) を参照してください。

## 構造

```shell
server
├── assets       // 内蔵静的アセット（CSS、JS、画像）
├── config       // サーバー設定
├── logger       // ロギングシステム
├── middleware   // サーバーミドルウェアコンポーネント
├── proto        // API リクエスト/レスポンス定義
├── router       // API ルートハンドラ
├── service      // コアサービスの実装
├── templates    // Go templ ページテンプレート
├── utils        // ユーティリティ関数
└── main.go
```

## 設定

設定ファイルのパスは `/etc/kvm/server.yaml` です。

```yaml
proto: http
port:
    http: 80
    https: 443
cert:
    crt: server.crt
    key: server.key

# ログレベル (debug/info/warn/error)
# 注意: 本番環境では 'info' または 'error' を使用し、'debug' は開発環境でのみ使用してください
logger:
    level: info
    file: stdout

# 認証設定 (enable/disable)
# 注意: 認証を無効にするのは開発環境でのみ行ってください
authentication: enable

jwt:
   # JWT 秘密鍵の設定。 空のままにすると、サーバー起動時にランダムな 64 バイトの鍵が自動的に生成されます。
   secretKey: ""
   # JWT トークンの有効期限（秒単位）。 デフォルト: 2678400 (31 日)
   refreshTokenDuration: 2678400
   # ユーザーがログアウトすると、すべての JWT トークンが無効になります。 デフォルト: true
   revokeTokensOnLogout: true
```

## コンパイルとデプロイ

注意: Go ツールチェーンが必要です。Go のクロスコンパイルに対応した任意のプラットフォームで使用可能です。

1. プロジェクトのコンパイル
    1. プロジェクトのルートディレクトリから `cd server` を実行します。
    2. `go mod tidy` を実行して Go の依存関係をインストールします。
    3. `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build` を実行してプロジェクトをコンパイルします。
    4. コンパイルが完了すると、`NanoKVM-Server` という名前の実行ファイルが生成されます。

2. アプリケーションのデプロイ
    1. デプロイ前に、ブラウザでアプリケーションを最新バージョンに更新します。手順は[こちら](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/system/updating.html)を参照してください。
    2. コンパイルして生成された `NanoKVM-Server` ファイルを使用して、NanoKVM の `/kvmapp/server/` ディレクトリ内の元のファイルを置き換えます。
    3. NanoKVM で `/etc/init.d/S95nanokvm restart` を実行してサービスを再起動します。

## 手動更新

> ファイルのアップロードには SSH が必要です。Web 設定で有効にしてください: `設定 > SSH`

1. [GitHub](https://github.com/sipeed/NanoKVM/releases) から最新のアプリケーションをダウンロードします。
2. ダウンロードしたファイルを解凍し、解凍したフォルダーの名前を `kvmapp` に変更します。
3. NanoKVM 上の既存の `/kvmapp` ディレクトリをバックアップし、新しい `kvmapp` フォルダーに置き換えます。
4. NanoKVM で `/etc/init.d/S95nanokvm restart` を実行してサービスを再起動します。
