{ pkgs, pkgs-unstable }:

pkgs.mkShell {
  packages = [
    pkgs-unstable.go_1_26
    (pkgs.writeShellScriptBin "cobra-cli" ''
      exec ${pkgs-unstable.cobra-cli}/bin/cobra-cli \
        -l MIT \
        --author "$(git config get user.name)" \
        "$@"
    '')
  ];
}
