{ pkgs, pkgs-unstable }:

pkgs.mkShell {
  packages = [
    pkgs-unstable.go_1_26
    pkgs-unstable.cobra-cli
  ];
}
