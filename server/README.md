# NanoKVM Server

This is the backend server implementation for NanoKVM.

For detailed documentation, please visit our [Wiki](https://wiki.sipeed.com/nanokvm).

## Structure

```shell
server
├── assets       // Embedded static assets (CSS, JS, images)
├── config       // Server configuration
├── logger       // Logging system
├── middleware   // Server middleware components
├── proto        // API request/response definitions
├── router       // API route handlers
├── service      // Core service implementations
├── templates    // Go templ page templates
├── utils        // Utility functions
└── main.go
```

## Configuration

The configuration file path is `/etc/kvm/server.yaml`.

```yaml
# Network Settings
proto: http            # Access protocol. Can be changed to `https` only when certificates are configured. Default is `http`
port:
    http: 80           # The listening port for the HTTP service. Default is `80`
    https: 443         # The listening port for the HTTPS service (effective when HTTPS is enabled). Default is `443`
cert:
    crt: server.crt    # The path to the public key certificate for HTTPS
    key: server.key    # The path to the private key file for HTTPS


# Logging Configuration
logger:
    level: info                          # Global log output level. Evaluated options from highest to lowest detail: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`. Default is `info`
    file: /var/log/NanoKVM-Server.log    # Log output destination. A file path directs log output to that file, which is rotated automatically (10 MB per file, 3 compressed backups, 28-day retention). `console` outputs to stdout instead. Default is `/var/log/NanoKVM-Server.log`


# Authentication & Security
authentication: enable              # Whether to enable identity verification for HTTP API and Web endpoints. Options are `enable` or `disable`. Default is `enable`. Highly recommended to leave this enabled for internet-facing devices!
jwt:
   secretKey: ""                    # The secret key used to sign and verify JWT Tokens. If left empty, a random key will be generated automatically on startup
   refreshTokenDuration: 2678400    # The token refresh duration threshold in seconds before forcing a re-login. Default is `2678400` (~31 days)
   revokeTokensOnLogout: true       # Whether to invalidate all existing tokens upon logout by rotating the SecretKey. Default is `true`
security:
   loginLockoutDuration: 0,         # The duration (in seconds) to ban an IP from attempting to log in again after reaching the failure limit. If set to `0` or left empty, brute-force protection is disabled. Default is `0`
   loginMaxFailures:     5,         # The maximum number of continuous failed login attempts allowed per IP before triggering protection. Default is `5`
```

## Compile & Deploy

Note: Use Linux operating system (x86-64), or any platform with Go cross-compilation support.

1. Compile the Project
    1. Run `cd server` from the project root directory.
    2. Run `go mod tidy` to install Go dependencies.
    3. Run `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build` to compile the project.
    4. After compilation, an executable file named `NanoKVM-Server` will be generated.

2. Deploy the Application
    1. File uploads requires SSH. Please enable it in the Web Settings: `Settings > SSH`;
    2. Replace the original file in the NanoKVM `/kvmapp/server/` directory with the newly compiled `NanoKVM-Server`.
    3. Restart the service on NanoKVM by executing `/etc/init.d/S95nanokvm restart`.

## Manually Update

> File uploads requires SSH. Please enable it in the Web Settings: `Settings > SSH`;

1. Download the latest application from [GitHub](https://github.com/sipeed/NanoKVM/releases);
2. Unzip the downloaded file and rename the unzipped folder to `kvmapp`;
3. Back up the existing `/kvmapp` directory on your NanoKVM, then replace it with the new `kvmapp` folder;
4. Run `/etc/init.d/S95nanokvm restart` on your NanoKVM to restart the service.
