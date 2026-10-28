{
  description = "Development shell for atte";

  inputs = {
    # 1. Check statuses of channels and Hydra before updating
    #   - https://nixos.wiki/wiki/Nix_channels
    #   - https://status.nixos.org/
    # 2. Update the link to the selected Hydra evaluation
    # 3. Document if the revision differs from the Hydra evaluation
    #   - For example, a slightly newer revision may be needed to pick up a patch

    #### Main stable release branch
    # https://hydra.nixos.org/jobset/nixos/release-26.05/evals
    nixpkgs = {
      # https://hydra.nixos.org/eval/1830478#tabs-inputs
      url = "github:NixOS/nixpkgs/cd4ea8d57de16f4810bc5f14207bac45fbf19f67";
      # url = "nixpkgs/nixos-26.05";
    };

    #### Unstable release branch
    # https://hydra.nixos.org/jobset/nixos/unstable
    nixpkgs-unstable = {
      # https://hydra.nixos.org/eval/1830477#tabs-inputs
      url = "github:NixOS/nixpkgs/02c087f3fe673c9e317f206a627cb6b910336dbe";
      # url = "nixpkgs/nixos-unstable";
    };

    #### Master branch (for cacerts)
    nixpkgs-master = {
      url = "github:NixOS/nixpkgs/f903b88037954e42a165c27b686201c2f242ae28";
    };

    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-unstable,
      nixpkgs-master,
      flake-utils,
      ...
    }:

    flake-utils.lib.eachSystem
      [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ]
      (
        system:
        let
          pkgs = import nixpkgs {
            inherit system;
            config.allowUnfreePredicate = pkg: nixpkgs.lib.getName pkg == "claude-code";
          };
          pkgs-unstable = import nixpkgs-unstable {
            inherit system;
            config.allowUnfreePredicate = pkg: nixpkgs.lib.getName pkg == "claude-code";
          };
          pkgs-master = import nixpkgs-master {
            inherit system;
          };
          cobraCli = pkgs.writeShellScriptBin "cobra-cli" ''
            exec ${pkgs-unstable.cobra-cli}/bin/cobra-cli \
              -l MIT \
              --author "$(git config get user.name)" \
              "$@"
          '';
        in
        {
          packages = {
            cobra-cli = cobraCli;
          };

          apps = {
            cobra-cli = {
              type = "app";
              program = "${cobraCli}/bin/cobra-cli";
            };
          };

          devShells.default = import ./shell.nix {
            inherit
              pkgs
              pkgs-unstable
              pkgs-master
              cobraCli
              ;
          };
        }
      );
}
