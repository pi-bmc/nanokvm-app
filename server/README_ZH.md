# NanoKVM Server

NanoKVM 后端服务的代码。更多文档请参考 [Wiki](https://wiki.sipeed.com/nanokvm) 。

## 目录结构

```shell
server
├── assets       // 内嵌静态资源（CSS、JS、图片）
├── config       // 服务配置
├── logger       // 服务日志
├── middleware   // 中间件
├── proto        // api 请求响应参数
├── router       // api 路由
├── service      // api 处理逻辑
├── templates    // Go templ 页面模板
├── utils        // 工具函数
└── main.go
```

## 配置文件

配置文件路径为 `/etc/kvm/server.yaml`。

```yaml
# 网络设置
proto: http            # 访问协议，默认为 `http`，仅当配置了证书时支持改为 `https`
port:
    http: 80           # HTTP 服务的监听端口，默认为 `80`
    https: 443         # HTTPS 服务的监听端口（启用 https 协议时生效），默认为 `443`
cert:
    crt: server.crt    # HTTPS 服务使用的公钥证书路径
    key: server.key    # HTTPS 服务使用的私钥文件路径


# 日志配置
logger:
    level: info     # 全局日志打印级别，从高到底可选 `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`。默认为 `info`
    file: stdout    # 日志输出目标位置。若填写 `stdout` 则输出在控制台。配置为文件路径则会输出到对应的文件。默认为 `stdout`


# 认证与安全
authentication: enable              # 是否开启 HTTP 接口与网页的身份校验。可选 `enable` (开启) 或 `disable` (禁用)。默认为 `enable`。强烈建议公开在互联网的机器开启此项！
jwt:
   secretKey: ""                    # 用于签发和验证 JWT Token 的密钥。如果不填，服务启动时将自动随机生成
   refreshTokenDuration: 2678400    # 登录超时的刷新周期（单位：秒）。默认为 `2678400`（约31天）
   revokeTokensOnLogout: true       # 退出登录时是否废除所有现存的 Token。启用此项可以在注销时轮换 SecretKey，强迫所有终端重新登录。默认为 `true`
security:
   loginLockoutDuration: 0,         # 达到失败上限后，禁止该 IP 再次尝试登录的持续时间（单位：秒）。如果设为 `0` 或不填，则代表不开启防暴力破解功能。默认为 `0`
   loginMaxFailures:     5,         # 允许触发保护前，单个 IP 连续登录失败的最大次数。默认为 `5`
```

## 编译部署

**注意：需要 Go 工具链，支持任何可进行 Go 交叉编译的平台。**

1. 编译
   1. 在项目根目录下执行 `cd server` 进入 server 目录；
   2. 执行 `go mod tidy` 安装 Go 依赖包；
   3. 执行 `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build` 进行编译；
   4. 编译完成后，会生成可执行文件 `NanoKVM-Server`。

2. 部署
   1. 上传文件需要启用 SSH 功能。请在 Web `设置 - SSH` 中检查 SSH 是否已经启用；
   2. 使用编译生成的 `NanoKVM-Server` 文件，替换 NanoKVM 中 `/kvmapp/server/` 目录下的原始文件；
   3. 在 NanoKVM 中执行 `/etc/init.d/S95nanokvm restart` 重启服务。

## 手动更新

> 请确保已经在 Web 界面的 `设置 - SSH` 中启用了 SSH 功能，以便上传文件。

1. 从 [GitHub](https://github.com/sipeed/NanoKVM/releases) 下载最新的应用安装包；
2. 解压缩下载的安装包，并将解压后的文件夹重命名为 `kvmapp`；
3. 备份 NanoKVM 系统中的 `/kvmapp` 目录，然后用解压后的 `kvmapp` 文件夹替换现有目录。
4. 在 NanoKVM 中执行 `/etc/init.d/S95nanokvm restart` 重启服务。
