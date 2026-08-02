#!/usr/bin/env python3
"""Set a Serena project language-server executable path."""

import argparse
import os
import pathlib

from ruamel.yaml import YAML
from ruamel.yaml.scalarstring import DoubleQuotedScalarString


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("language_server")
    parser.add_argument("executable")
    args = parser.parse_args()

    os.chdir(os.environ["ATTE_DEV_DIR"])
    project_file = pathlib.Path.cwd() / ".serena" / "project.yml"
    yaml = YAML()
    yaml.preserve_quotes = True
    with project_file.open() as stream:
        project = yaml.load(stream)

    language_servers = project.setdefault("ls_specific_settings", {})
    language_server = language_servers.setdefault(args.language_server, {})
    setting = "shellcheckPath" if args.language_server == "bash" else "ls_path"
    language_server[setting] = DoubleQuotedScalarString(args.executable)

    with project_file.open("w") as stream:
        yaml.dump(project, stream)

    print(f"updated {project_file} to use {args.executable}")


if __name__ == "__main__":
    main()
