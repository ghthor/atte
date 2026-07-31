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
  };

  outputs =
    { nixpkgs, nixpkgs-unstable, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      devShells = forAllSystems (system: {
        default = import ./shell.nix {
          pkgs = import nixpkgs { inherit system; };
          pkgs-unstable = import nixpkgs-unstable { inherit system; };
        };
      });
    };
}
