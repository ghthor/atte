lint "actionlint" {
  script = <<EOF
shopt -s nullglob
if command -v actionlint >/dev/null 2>&1; then
  actionlint *.yml *.yaml
else
  nix develop --command actionlint *.yml *.yaml
fi
EOF
}
