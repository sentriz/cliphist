### cliphist

_clipboard history “manager” for wayland_

- write clipboard changes to a history file
- recall history with **dmenu** / **rofi** / **wofi** (or whatever other picker you like)
- both **text** and **images** are supported
- clipboard is preserved **byte-for-byte**
  - leading / trailing whitespace / no whitespace or newlines are
    preserved
  - won’t break fancy editor selections like vim wordwise, linewise,
    block mode
- no concept of a picker, only pipes

requires: [go](https://golang.org/),
[wl-clipboard](https://github.com/bugaevc/wl-clipboard), xdg-utils (for
image mime inferance)

---

### install

- you could try using [your distro's repos](#packaging) if it's available there
- or stick a static binary from the [releases page](https://github.com/sentriz/cliphist/releases) somewhere in your `$PATH`
- or just install it from source with [go](https://go.dev/doc/install) and `$ go install go.senan.xyz/cliphist@latest`

---

### usage

#### listen for clipboard changes

`$ wl-paste --watch cliphist store`  
this will listen for changes on your primary keyboard and write it to
the history.  
call it once per session - for example in your sway config

#### select old item

`$ cliphist list | dmenu | cliphist decode | wl-copy`  
bind it to something nice on your keyboard

#### delete old item

`$ cliphist list | dmenu | cliphist delete`  
or else query manually  
`$ cliphist delete-query "secret item"`

#### clear database

`$ cliphist wipe`

---

### picker examples

<details>
<summary>dmenu</summary>

`cliphist list | dmenu | cliphist decode | wl-copy`

</details>

<details>
<summary>fzf</summary>

`cliphist list | fzf | cliphist decode | wl-copy`

</details>

<details>
<summary>rofi (dmenu mode)</summary>

`cliphist list | rofi -dmenu | cliphist decode | wl-copy`

</details>

<details>
<summary>rofi (custom mode)</summary>

`rofi -modi clipboard:/path/to/cliphist-rofi -show clipboard`

(requires [contrib/cliphist-rofi](https://github.com/sentriz/cliphist/blob/master/contrib/cliphist-rofi))

</details>

<details>
<summary>rofi (custom mode with images)</summary>

`rofi -modi clipboard:/path/to/cliphist-rofi-img -show clipboard -show-icons`

(requires [contrib/cliphist-rofi-img](https://github.com/sentriz/cliphist/blob/master/contrib/cliphist-rofi-img))

</details>

<details>
  <summary>wofi</summary>

  `cliphist list | wofi -S dmenu | cliphist decode | wl-copy`

  Example config for sway:
```
exec wl-paste --watch cliphist store
bindsym Mod1+p exec cliphist list | wofi -S dmenu | cliphist decode | wl-copy
```
</details>

---

### faq

<details>
<summary><strong>why do i have numbers in my picker? can i get rid of them?</strong></summary>

it's important that a line prefixed with a number is piped into `cliphist decode`. this number is used to lookup in the database the exact original selection that you made, with all leading, trailing, non printable etc whitespace presevered. none of that will not be shown in the preview output of `cliphist list`

since the format of `cliphist list` is `"<id>\t<100 char preview>"`, and most pickers consider `"\t"` to be column seperator, you can try to just select column number 2

```shell
# fzf
cliphist list | fzf -d $'\t' --with-nth 2 | cliphist decode | wl-copy
```

```shell
# rofi
cliphist list | rofi -dmenu -display-columns 2 | cliphist decode | wl-copy
```

```shell
# wofi
# it kind of works but breaks with quotes in the original selection. i recommend not trying to hide the column with wofi
cliphist list | wofi --dmenu --pre-display-cmd "echo '%s' | cut -f 2" | cliphist decode | wl-copy
```

</details>

<details>
<summary><strong>how do i narrow down the items that are copied to cliphist, or always copy images from my browser?</strong></summary>

it's also possible to run `wl-paste --watch` several times for multiple mime types

for example in your window manager's startup you could run

```
wl-paste --type text --watch cliphist store
wl-paste --type image --watch cliphist store
```

now you should have text and raw image data available in your history. make sure you have xdg-utils installed too

</details>

---

### packaging

[![](https://repology.org/badge/vertical-allrepos/cliphist.svg?columns=4)](https://repology.org/project/cliphist/versions)

---

### video

<https://user-images.githubusercontent.com/6832539/230513908-b841fffe-d7d5-46c2-b29f-28b3e91daa74.mp4>
