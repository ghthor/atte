codegen "actions-lock" {
  script = <<EOF
cd $(git rev-parse --show-toplevel)
gh extension install github/gh-actions-lock
if [ "$CI" = "true" ]; then
  gh actions-lock --verify
else
  gh actions-lock
fi
EOF
}

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
