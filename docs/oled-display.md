# Front-panel OLED status display

An optional 0.96" 128x64 SSD1306 I2C OLED that shows, at the machine, what the
BMC knows about it: whether the host is on, how to reach the BMC, and anything
that is wrong. Written for the two-colour modules whose top 16 rows are yellow
and remaining 48 blue; a single-colour module shows the same layout untinted.

Code: `pkg/device/oled` (renderer, service) and `pkg/device/oled/ssd1306`
(driver). The status it shows is gathered in `cmd/server/display.go`.

## Wiring

The display has a bus of its own, IIC3, on two otherwise unused header pads.

| Module pin | Board pad | Header label | Function |
| --- | --- | --- | --- |
| GND | GND | GND | |
| VCC | 3V3 | 3V3 | **not 5V** |
| SCL | `SD1_CMD` | P22 | IIC3_SCL (pad function 2) |
| SDA | `SD1_CLK` | P23 | IIC3_SDA (pad function 2) |

- **Power the module from 3V3.** The SG2002 pads are not 5V tolerant, and some
  of these modules pull SDA/SCL up to VCC rather than to their own regulator.
- **Check the module's pin order before plugging it in.** Most are
  `GND VCC SCL SDA`; some batches swap the first two.
- Leave the address strap at its default, `0x3C` (silkscreened as `0x78`, the
  same address in 8-bit form). The other strap, `0x3D`, works but shares its
  address with a placeholder client the capture stack's `soph_snsr_i2c` module
  registers on every adapter.
- No external pull-ups are needed: the module carries its own, and the pads'
  internal pull-ups hold the bus idle when nothing is plugged in.

The bus needs kernel support: `nanokvm-build` patch
`0013-riscv-dts-sophgo-nanokvm-enable-i2c3-oled.patch` enables `i2c@4030000`
on those pads and pins it at `/dev/i2c-3`. On an image without it the app logs
`display: i2c bus not present on this board; display disabled` once and does
nothing further.

The app does not rely on that number. By default it looks the bus up by its
controller (`/sys/bus/platform/devices/4030000.i2c/i2c-N`), for the same
reason GPIO chips are resolved by label: a bus number identifies nothing by
itself, and the first thing the display service does to a bus is write to it.
Run on a machine without that controller — a developer's workstation, where
"bus 3" may be the SMBus — it opens nothing.

## Why IIC3: the board's pin map

Every I2C bus and GPIO line the BMC uses, and why the display could not share
one.

| Bus / line | Pads (header) | Used for | Could it carry the display? |
| --- | --- | --- | --- |
| `i2c1` → `/dev/i2c-0` | `SD1_D3` SCL (P18), `SD1_D0` SDA (P21) | **Slave**: the 24c512 EEPROM at 0x50 the managed host pushes sensor records into (`pkg/device/bmcsensor`) | No. A DesignWare controller is a master or a slave; in slave mode it cannot address a panel. The host is the master on that wire. |
| `i2c4` → `/dev/i2c-4` | `PWR_WAKEUP0` SCL, `PWR_BUTTON1` SDA (CSI connector) | LT6911C HDMI bridge at 0x2B, owned by the kernel (`soph_v4l2`) | No. 1.8V-only pads, not on the header, and the module pulls the bus to 3.3V. |
| **`i2c3` → `/dev/i2c-3`** | **`SD1_CMD` SCL (P22), `SD1_CLK` SDA (P23)** | **The display, 0x3C** | Free 3.3V header pads, same pad type and supply domain as the working `i2c1` pair. |
| `gpiochip` 3020000.gpio line 23 | `EMMC_CMD` (A23) | `power-button`, open-drain output | — |
| line 24 | `EMMC_DAT1` (A24) | `power-led`, sense input | — |
| line 25 | `EMMC_DAT0` (A25) | `hdd-led` (alpha) / reset (beta, pcie) | — |
| line 27 | `EMMC_DAT3` (A27) | `reset-button` (alpha), open-drain output | — |
| `uart0` | `UART0_TX/RX` (A16/A17) | BMC console | — |
| `uart1` → `/dev/ttyS1` | `JTAG_CPU_TMS/TCK` (A19/A18) | Host serial console | — |
| 5021000.gpio line 1 | RTC domain | LT6911C reset (`snsr-rst-gpio`) | — |

Pads still unclaimed after this: `SD1_D2`/`SD1_D1` (P19/P20 — UART2, or an
alternative route for IIC1), `IIC0_SCL`/`IIC0_SDA` (A28/A29), `EMMC_CLK` (A22)
and `EMMC_DAT2` (A26).

GPIO lines are resolved by device-tree line name (`pkg/config/hardware.go`),
and pinctrl pin numbers are package pin numbers, not GPIO offsets — `EMMC_CMD`
is pinctrl pin 24 but GPIO line 23. The table above uses GPIO offsets.

## What it shows

```text
+----------------------------+
| licheervnano         * ON  |  yellow band: BMC hostname, host power
+----------------------------+
| 10.0.197.82                |  blue area: one page at a time,
| eth0 100M dhcp             |  three lines of 18 characters
| 48:da:35:6f:f2:e0          |
+----------------------------+
```

The header never changes place. Its lamp is a solid disc when the host is on
and a ring when it is off; `--` means the BMC cannot sense the host's power at
all (unwired header). An inverse `!` appears beside it while any alert is
active.

The body rotates through:

| Page | Lines |
| --- | --- |
| Network | IPv4 address (`DHCP...` while waiting, `No link` without carrier) · interface, speed, mode · MAC |
| Host | Power and boot phase from the host firmware's Redfish BootProgress (`OS running`, `in setup`, …) · SoC temperature and fan duty, when the board has a sensor channel and the host is reporting · remote-console viewers, else HDMI mode, else model |
| BMC | App version · uptime · CPU and memory |
| Alerts | Only while there are any, and first in the rotation: under-voltage, over-temperature, throttling, frequency cap |

A power transition takes over the body for three seconds with a double-size
`POWER ON` / `POWER OFF`, then lands on the Host page. A new alert or a new IP
address jumps straight to its page.

## Configuration

`/etc/kvm/server.yaml`:

```yaml
display:
  enabled: true        # false: never open the bus
  device: ""           # empty: the board's IIC3 bus. Or a path, e.g. /dev/i2c-1
  address: "0x3c"
  flip: false          # true: module mounted with its pin header at the bottom
  brightness: 143      # contrast, 1..255
  pageSeconds: 5       # how long each page stays up
  dimAfterMinutes: 10  # 0 = never dim
```

It defaults on: with nothing plugged in the cost is one unacknowledged I2C
transfer every ten seconds on a bus nothing else uses. Changes take effect on
the next app start.

`flip` rotates the image in the controller. On a yellow/blue panel that puts
the yellow band at the bottom, and the header moves there with it so it is
still the part drawn in yellow.

## Behaviour worth knowing

- **Hot-plug.** An absent panel is probed every 10 seconds; plugging one in
  brings it up without restarting anything. Unplugging is logged once.
- **Self-repair.** The controller cannot be read back, so a module that
  browned out and reset still acknowledges writes while showing nothing. Once a
  minute the panel is reprogrammed and repainted from scratch — invisible on a
  healthy panel.
- **Bus traffic.** The driver keeps a shadow of the display RAM and sends only
  the columns that changed. A static page sends nothing.
- **Wear.** OLEDs age where they are lit. The panel dims to its lowest readable
  level after `dimAfterMinutes` without an event (power transition, new
  address, alert) and returns to full brightness on the next one; it stays
  bright while an alert is showing. The whole image also orbits by one pixel
  once a minute. It is never switched off while the app runs — there is no
  button to wake it.
- **Shutdown.** The panel is blanked when the app stops, so a stale power state
  is not left on the glass. After a hard kill the last frame stays until the
  app restarts.
- Nothing is ever drawn across the yellow/blue boundary; the two zones are
  separate glass with a gap between them.

## Working on it without a display

Rendering is a pure function of an `oled.Status`, so every screen can be drawn
to a PNG, tinted and spaced like the real glass:

```sh
OLED_PREVIEW_DIR=/some/dir go test ./pkg/device/oled -run TestPreview
```

`TestGoldenFrames` pins the screens pixel for pixel; after an intentional
layout change, regenerate with `-update` and look at the previews before
committing the result.

Only SSD1306-compatible 128x64 controllers are supported (including SSD1315).
The 1.3" modules are usually SH1106, which addresses its RAM differently and
will show a shifted, torn image.
