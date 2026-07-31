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
      # https://hydra.nixos.org/eval/1827033#tabs-inputs
      url = "github:NixOS/nixpkgs/8f0500b9660505dc3cb647775fe9a978a74b5283";
      # url = "nixpkgs/nixos-26.05";
    };

    #### Unstable release branch
    # https://hydra.nixos.org/jobset/nixos/unstable
    nixpkgs-unstable = {
      # https://hydra.nixos.org/eval/1827505#tabs-inputs
      url = "github:NixOS/nixpkgs/624af665418d3c65d544145b4d34ad696439570e";
      # url = "nixpkgs/nixos-unstable";
    };

    # Pin the Nucleus source to a reproducible remote revision.
    nucleus.url = "github:sig-id/nucleus/dbe87fa29879bfee059a5d708811a3da74bb9225";

    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-unstable,
      nucleus,
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
          cobraCli = pkgs.writeShellScriptBin "cobra-cli" ''
            exec ${pkgs-unstable.cobra-cli}/bin/cobra-cli \
              -l MIT \
              --author "$(git config get user.name)" \
              "$@"
          '';
          agentToolchainRootfs = nucleus.lib.mkAgentToolchainRootfs {
            inherit pkgs;
            # TODO: this needs to ref my system wrapped claude package somehow, maybe we need to move this into ~/src/shrc/nix home-manager?
            providerPackages = [ pkgs-unstable.claude-code ];
            extraPackages = [ cobraCli ];
            name = "atte-agent-toolchain-rootfs";
          };
          nucleusPackage = nucleus.packages.${system}.default.overrideAttrs (_: {
            doCheck = false;
          });
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
              agentToolchainRootfs
              nucleusPackage
              cobraCli
              ;
          };
        }
      );
}
