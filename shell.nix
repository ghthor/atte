{
  pkgs,
  pkgs-unstable,
  pkgs-master,
  cobraCli,
}:

pkgs.mkShell {
  packages = [
    (pkgs.writeShellApplication {
      name = "atte-configure-serena";
      runtimeInputs = [ pkgs.uv ];
      text = ''
        : "''${ATTE_DEV_DIR:?ATTE_DEV_DIR must be set}"
        tools_dir="$(git -C "$ATTE_DEV_DIR" rev-parse --show-toplevel)/tools"
        uv run --directory "$tools_dir" \
          python serena-set-lsp.py markdown "${pkgs.marksman}/bin/marksman"
        uv run --directory "$tools_dir" \
          python serena-set-lsp.py bash "${pkgs.shellcheck}/bin/shellcheck"
      '';
    })
    (pkgs.writeShellApplication {
      name = "atte";
      runtimeInputs = [ pkgs-unstable.go_1_26 ];
      text = ''
        : "''${ATTE_DEV_DIR:?ATTE_DEV_DIR must be set}"
        exec go run "$ATTE_DEV_DIR" "$@"
      '';
    })
    pkgs-unstable.go_1_26
    pkgs.gotools
    pkgs-unstable.golangci-lint
    pkgs.treefmt
    pkgs.nixfmt
    pkgs.hclfmt
    pkgs.graph-easy
    pkgs.marksman
    pkgs.shellcheck
    pkgs.uv
    cobraCli
  ];

  shellHook = ''
    export UV_NO_MANAGED_PYTHON=1
    export NIX_SSL_CERT_FILE=${pkgs-master.cacert}/etc/ssl/certs/ca-bundle.crt

    if [ -z "''${ATTE_DEV_DIR:-}" ]; then
      export ATTE_DEV_DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
    fi

    bash_completion_dir="''${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"
    mkdir -p "$bash_completion_dir"
    atte completion bash > "$bash_completion_dir/atte"

    zsh_completion_dir="''${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
    mkdir -p "$zsh_completion_dir"
    atte completion zsh > "$zsh_completion_dir/_atte"

    if [ -n "''${PS1:-}" ]; then
      printf 'atte development shell: %s\n' "$ATTE_DEV_DIR" >&2
    fi
  '';
}
