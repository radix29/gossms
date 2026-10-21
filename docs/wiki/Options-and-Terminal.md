[← goSSMS Home](https://github.com/radix29/gossms/wiki)

# Options and the terminal

What you can turn, where goSSMS keeps it, and what a terminal does to the keys and colours
you think you asked for.

## Tools ▸ Options

Seven settings, applied **live** — no restart, and no screen tear:

| | |
|---|---|
| **Color Theme** | `Dark (default)` / `Light` / `Auto (follow terminal)`. The active style is read from the palette at draw time, so switching is a palette swap and the tree repaints in place. Picked up from `NO_COLOR` (any non-empty value means dark) and `COLORFGBG` (a `…;N` whose background index is light means light) |
| **Object Explorer Icons** | `Colour` / `Monochrome` — every glyph in one palette colour, drawn **before** state marks so `(No access)` and `(Disabled)` stay legible. A monochrome or ASCII-forced terminal would otherwise render colour glyphs as `?` |
| **Indent size (spaces)** | The editor's tab width, pushed into every open query panel and property sheet immediately, and used for new ones |
| **Max default cell length** | The results grid's per-cell cap |
| **Max characters per column (text)** | The cap for text output — the `varchar(max)` ceiling |
| **Extended Events viewer holds (events)** | How many events the live viewer keeps, which is what bounds its memory on a chatty session |
| **IntelliSense** | On or off for the whole feature |

`OK` and `Apply` write the config; `Esc` closes without applying. The indent size is the one
setting with a second step behind it — it lives in each editor, so the dialog pushes it into
the panels that are already open rather than waiting for them to be recreated.

## Where the state lives

| | |
|---|---|
| **Config and saved connections** | `<user config dir>/gossms/config.json` — one file holding both the settings above and the connection history, so a machine's worth of servers and a personal theme travel together. Connections are addressed by server and instance, the way Object Explorer shows them |
| **Log** | `<user config dir>/gossms/gossms.log`, rotated at a size cap; the path is shown in **Help ▸ About**. Logging is on by default and needs no environment variable |
| **Recovered query text** | Written when the process would otherwise lose it: see [Query Editor](https://github.com/radix29/gossms/wiki/Query-Editor) |

The user config directory is `%APPDATA%` on Windows, `$XDG_CONFIG_HOME` or `~/.config` on
Linux, `~/Library/Application Support` on macOS.

## The terminal is part of the product

goSSMS is a terminal application, and the terminal decides what it can promise.

- **24-bit colour.** `COLORTERM=truecolor|24bit` — or a `TERM` value that is known to have
  it, such as `xterm-256color`, `alacritty`, `foot-direct` or `wezterm`. Windows Terminal
  advertises it through `WT_SESSION`. Without true colour the 16 ANSI colours are used and
  the theme reads *roughly* rather than as designed: goSSMS themes in the Delft palette
  family, whose shades sit between the box colours rather than on them.
- **Mouse.** Mouse reporting is enabled for the whole window, so click, drag, resize and the
  wheels work in the tree, the grids, the splitters and the tabs. The price is that the
  terminal no longer hands you its own select-to-copy: copy is in-app (**Ctrl+C** where a
  copy would mean that — `Shift+Click` to follow a URL), and the terminal's own copy is still
  available with `Shift` held where the terminal keeps a key for it.
- **Keys.** See [Keys and mouse](https://github.com/radix29/gossms/wiki/Keys-and-Mouse): a
  window manager, a terminal multiplexer or an IME can claim a chord before goSSMS sees it.
  **Help ▸ Key Diagnostics** prints every key and mouse event with its modifiers and timing,
  which is the difference between a wrong binding and a key your terminal kept.
- **A terminal that quits on a dropped connection** (a closed SSH session, a window closed
  out from under the process) gets an emergency save rather than a lost buffer, because the
  alternative is a rule nobody learns before they need it.
- **Performance** is a per-frame budget rather than a polling loop: the screen redraws when
  something changes, and a 100,000-row grid that is not on screen is not drawn. The result is
  that idle CPU is what your terminal's own blinking cursor costs, and a query that returns a
  hundred thousand rows streams into a grid instead of freezing it.

If the mouse does not work, set `TERM` to `xterm-256color`, or use Windows Terminal; if the
colours look off, check `COLORTERM`; if a key does nothing, check Key Diagnostics before
blaming the app.

## See also

- [Keys and mouse](https://github.com/radix29/gossms/wiki/Keys-and-Mouse)
- [Connecting](https://github.com/radix29/gossms/wiki/Connecting) — what is saved, and how secrets are kept
- [Query Editor](https://github.com/radix29/gossms/wiki/Query-Editor)

[← Home](https://github.com/radix29/gossms/wiki)
