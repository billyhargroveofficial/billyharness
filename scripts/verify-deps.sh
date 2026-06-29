#!/usr/bin/env bash
set -euo pipefail

script_dir="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(CDPATH= cd -- "$script_dir/.." && pwd)"
cd "$repo_root"

go_bin="${GO_BIN:-go}"
module_path="$(awk '$1 == "module" { print $2; exit }' go.mod)"
want_go="$(awk '$1 == "go" { print $2; exit }' go.mod)"
got_go="$("$go_bin" env GOVERSION)"
got_go="${got_go#go}"

if [[ "$got_go" != "$want_go" ]]; then
	echo "go version mismatch: go.mod declares go$want_go but $go_bin is go$got_go" >&2
	exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
cp go.mod "$tmp_dir/go.mod"
cp go.sum "$tmp_dir/go.sum"

"$go_bin" mod tidy
if ! cmp -s go.mod "$tmp_dir/go.mod" || ! cmp -s go.sum "$tmp_dir/go.sum"; then
	echo "go mod tidy produced a diff; restore from the diff below and commit tidy metadata" >&2
	diff -u "$tmp_dir/go.mod" go.mod >&2 || true
	diff -u "$tmp_dir/go.sum" go.sum >&2 || true
	cp "$tmp_dir/go.mod" go.mod
	cp "$tmp_dir/go.sum" go.sum
	exit 1
fi

direct_required="$tmp_dir/direct-required.txt"
"$go_bin" list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}{{end}}' all |
	sed '/^$/d' |
	sort -u >"$direct_required"

direct_import_modules="$tmp_dir/direct-import-modules.txt"
{
	"$go_bin" list -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}{{range .TestImports}}{{.}}{{"\n"}}{{end}}{{range .XTestImports}}{{.}}{{"\n"}}{{end}}' ./... |
		sort -u |
		while IFS= read -r import_path; do
			[[ -n "$import_path" ]] || continue
			module="$("$go_bin" list -f '{{if .Standard}}{{else}}{{with .Module}}{{.Path}}{{end}}{{end}}' "$import_path")"
			[[ -n "$module" && "$module" != "$module_path" ]] || continue
			echo "$module"
		done
} | sort -u >"$direct_import_modules"

missing_direct="$tmp_dir/missing-direct.txt"
comm -23 "$direct_import_modules" "$direct_required" >"$missing_direct"
if [[ -s "$missing_direct" ]]; then
	echo "directly imported modules must be direct go.mod requirements:" >&2
	sed 's/^/  - /' "$missing_direct" >&2
	exit 1
fi

echo "dependency metadata verified with go$want_go"
