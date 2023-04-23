#!/usr/bin/env sh

if [ -z "$1" ]; then
    cliphist list
else
    cliphist decode <<<"$1" | wl-copy
fi
