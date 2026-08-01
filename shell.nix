{
  pkgs,
  pkgs-unstable,
  nucleusPackage,
  agentToolchainRootfs,
  cobraCli,
}:

pkgs.mkShell {
  packages = [
    pkgs-unstable.go_1_26
    pkgs-unstable.golangci-lint
    pkgs.treefmt
    pkgs.nixfmt
    pkgs.graph-easy
    # nucleusPackage
    cobraCli
    (pkgs.writeShellScriptBin "atte-agent" ''
      set -eu

      workspace="''${ATTE_AGENT_WORKSPACE:-$PWD}"
      exec ${nucleusPackage}/bin/nucleus run \
        --service-mode strict-agent \
        --agent-toolchain-rootfs ${agentToolchainRootfs} \
        --workspace "$workspace" \
        --workspace-exec \
        --provider-config-rw "$HOME/.claude:.claude" \
        -- claude "$@"
    '')
  ];
}
