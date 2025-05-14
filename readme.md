### cliphist

_Clipboard history “manager” for Wayland_

- Write clipboard changes to a history file.
- Recall history with **dmenu**, **rofi**, **wofi** (or whatever other picker you like).
- Both **text** and **images** are supported.
- Clipboard is preserved **byte-for-byte**.
  - Leading/trailing whitespace, no whitespace, or newlines are preserved.
  - Won’t break fancy editor selections like Vim wordwise, linewise, or block mode.
- No concept of a picker, only pipes.

Requires [Go](https://golang.org/), [wl-clipboard](https://github.com/bugaevc/wl-clipboard), xdg-utils (for image MIME inference).

---

### Install

- You could try using [your distro's repos](#packaging) if it's available there.
- Or stick a static binary from the [releases page](https://github.com/sentriz/cliphist/releases) somewhere in your `$PATH`.
- Or just install it from source with [Go](https://go.dev/doc/install) and `$ go install go.senan.xyz/cliphist@latest`.

---

### Usage

#### Listen for clipboard changes

`$ wl-paste --watch cliphist store`  
This will listen for changes on your primary clipboard and write them to the history.  
Call it once per session - for example, in your Sway config.

#### Select an old item

`$ cliphist list | dmenu | cliphist decode | wl-copy`  
Bind it to something nice on your keyboard.

#### Delete an old item

`$ cliphist list | dmenu | cliphist delete`  
Or else query manually:
`$ cliphist delete-query "secret item"`.

#### Clear database

`$ cliphist wipe`.

---

### Picker examples

<details>
<summary>dmenu</summary>

`cliphist list | dmenu | cliphist decode | wl-copy`

</details>

<details>
<summary>fzf</summary>

`cliphist list | fzf --no-sort | cliphist decode | wl-copy`

</details>

<details>
<summary>rofi (dmenu mode)</summary>

`cliphist list | rofi -dmenu | cliphist decode | wl-copy`

</details>

<details>
<summary>fuzzel (dmenu mode)</summary>

`cliphist list | fuzzel --dmenu | cliphist decode | wl-copy`

</details>

<details>
<summary>fuzzel (dmenu mode with images)</summary>

`./cliphist-fuzzel-img`

(Requires [contrib/cliphist-fuzzel-img](./contrib/cliphist-fuzzel-img))

</details>

<details>
<summary>rofi (custom mode)</summary>

`rofi -modi clipboard:/path/to/cliphist-rofi -show clipboard`

(Requires [contrib/cliphist-rofi](https://github.com/sentriz/cliphist/blob/master/contrib/cliphist-rofi)).

</details>

<details>
<summary>rofi (custom mode with images)</summary>

`rofi -modi clipboard:/path/to/cliphist-rofi-img -show clipboard -show-icons`

(Requires [contrib/cliphist-rofi-img](https://github.com/sentriz/cliphist/blob/master/contrib/cliphist-rofi-img)).

</details>

<details>
<summary>wofi</summary>

`cliphist list | wofi -S dmenu | cliphist decode | wl-copy`

Example config for Sway:

```
exec wl-paste --watch cliphist store
bindsym Mod1+p exec cliphist list | wofi -S dmenu | cliphist decode | wl-copy
```

</details>

---

### FAQ

<details>
<summary><strong>Why do I have numbers in my picker? Can I get rid of them?</strong></summary>

It's important that a line prefixed with a number is piped into `cliphist decode`. This number is used to look up in the database the exact original selection that you made, with all leading, trailing, non-printable, etc. whitespace preserved. None of that will be shown in the preview output of `cliphist list`.

Since the format of `cliphist list` is `"<id>\t<100 char preview>"`, and most pickers consider `"\t"` to be a column separator, you can try to just select column number 2.

```shell
# fzf
cliphist list | fzf -d $'\t' --with-nth 2 | cliphist decode | wl-copy
```

```shell
# rofi
cliphist list | rofi -dmenu -display-columns 2 | cliphist decode | wl-copy
```

```shell
# fuzzel
cliphist list | fuzzel --dmenu --with-nth 2 | cliphist decode | wl-copy
```

```shell
# wofi
# It kind of works but breaks with quotes in the original selection. I recommend not trying to hide the column with wofi.
cliphist list | wofi --dmenu --pre-display-cmd "echo '%s' | cut -f 2" | cliphist decode | wl-copy
```

</details>

<details>
<summary><strong>How do I narrow down the items that are copied to cliphist, or always copy images from my browser?</strong></summary>

It's also possible to run `wl-paste --watch` several times for multiple MIME types.

For example, in your window manager's startup, you could run:

```
wl-paste --type text --watch cliphist store
wl-paste --type image --watch cliphist store
```

Now you should have text and raw image data available in your history. Make sure you have xdg-utils installed too.

</details>

---

### Packaging

[![](https://repology.org/badge/vertical-allrepos/cliphist.svg?columns=4)](https://repology.org/project/cliphist/versions)

---

### Demo

<https://user-images.githubusercontent.com/6832539/230513908-b841fffe-d7d5-46c2-b29f-28b3e91daa74.mp4>

---

### Configuration

`cliphist` can be optionally configured to extend the default functionality. Any option can be provided with a CLI argument, environment variable, or config file key.

For example, the option `max-items`, can be set via the CLI as `-max-items 100`, as an environment variable `CLIPHIST_MAX_ITEMS=100`, or in the config file as `max-items 100`.

If you choose to use the config file, the default location is `$XDG_CONFIG_HOME/cliphist/config`. The format is a text file with one option per line, where each line is `<key> <value>`. For example:

```
# example cliphist config
max-items 1000
max-dedupe-search 200
```

The list of available options is:

| CLI argument       | Environment variable        | Config file key   | Description                                                                                   |
| ------------------ | --------------------------- | ----------------- | --------------------------------------------------------------------------------------------- |
| -max-dedupe-search | CLIPHIST_MAX_DEDEUPE_SEARCH | max-dedupe-search | (Optional) maximum number of last items to look through when finding duplicates (default 100) |
| -max-items         | CLIPHIST_MAX_ITEMS          | max-items         | (Optional) maximum number of items to store (default 750)                                     |
| -preview-width     | CLIPHIST_PREVIEW_WIDTH      | preview-width     | (Optional) maximum number of characters to preview (default 100)                              |
| -db-path           | CLIPHIST_DB_PATH            | db-path           | (Optional) path to db (default `$XDG_CACHE_HOME/cliphist/db`)                                 |
| -config-path       | CLIPHIST_CONFIG_PATH        |                   | (Optional) path to config (default `$XDG_CONFIG_HOME/cliphist/config`)                        |
