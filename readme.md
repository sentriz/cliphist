<sup>looking for
<a href="https://github.com/sentriz/cliphist-sh">cliphist-sh</a>?</sup>

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

### install

`$ go install go.senan.xyz/cliphist@latest`  
alternatively, static binaries can be found on the [releases
page](https://github.com/sentriz/cliphist/releases)

### usage

###### listen for clipboard changes

`$ wl-paste --watch cliphist store`  
this will listen for changes on your primary keyboard and write it to
the history.  
call it once per session - for example in your sway config

###### select old item

`$ cliphist list | dmenu | cliphist decode | wl-copy`  
bind it to something nice on your keyboard

###### delete old item

`$ cliphist list | dmenu | cliphist delete`  
or else query manually  
`$ cliphist delete-query "secret item"`

###### clear database

`$ cliphist wipe`

### packaging

[![](https://repology.org/badge/vertical-allrepos/cliphist.svg)](https://repology.org/project/cliphist/versions)
