#!/usr/bin/env bash

tmp_dir="/tmp/cliphist"
rm -rf "$tmp_dir"

if [[ -n "$1" ]]; then
	cliphist decode <<<"$1" | wl-copy
	exit
fi

mkdir -p "$tmp_dir"

cliphist list \
	| grep -Fv '<meta http-equiv="content-type"' \
	| while read -r data; do
	fname="$(cut -f1 <<<"$data")"
	fext="$(cut -f2 <<<"$data" | cut -d ' ' -f6)"
	case "$fext" in
		jpg|jpeg|png|bmp)
			cliphist decode <<<"$data" >|"$tmp_dir"/"$fname"."$fext" &
			echo -en "$data\0icon\x1f$tmp_dir/$fname.$fext\n"
			;;
		*)
			echo "$data"
			;;
	esac
done
