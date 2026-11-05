#!/usr/bin/env python3
"""Fail when an installer's dependency checks and the documentation disagree.

This is the single implementation of the drift guard described in #102. The
installer identifiers (``DEP_*`` in install.sh, ``$Dep*`` in install.ps1), the
glibc floors, the minimum Windows build, and the table in
``docs/dependencies.md`` are parsed here so the pytest suite and release CI
cannot check two subtly different things.

Stdlib only: release CI runs it with the system Python before any dependencies
are installed.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


def _read(repo_root: Path, relative: str) -> str:
    return (repo_root / relative).read_text(encoding="utf-8")


def installer_dependency_surface(repo_root: Path) -> tuple[set[str], set[str], dict[str, str], str]:
    """Parse the identifiers and floors the installers actually check.

    Formatting tolerance is deliberate: the values are the contract, not the
    spacing around the assignment.
    """

    install_sh = _read(repo_root, "install.sh")
    install_ps1 = _read(repo_root, "install.ps1")

    linux_ids = set(
        re.findall(r'^\s*DEP_[A-Z0-9_]+\s*=\s*["\']([^"\']+)["\']', install_sh, re.MULTILINE)
    )
    windows_ids = set(
        re.findall(
            r'^\s*\$Dep[A-Za-z0-9_]+\s*=\s*["\']([^"\']+)["\']',
            install_ps1,
            re.MULTILINE,
        )
    )
    floors = {
        name.lower(): value
        for name, value in re.findall(
            r'^\s*GLIBC_(CLIENT|SERVER)_FLOOR\s*=\s*["\']([^"\']+)["\']',
            install_sh,
            re.MULTILINE,
        )
    }
    minimum_build = re.search(r"^\s*\$MinimumWindowsBuild\s*=\s*(\d+)", install_ps1, re.MULTILINE)
    if minimum_build is None:
        raise ValueError("install.ps1 does not declare $MinimumWindowsBuild")
    return linux_ids, windows_ids, floors, minimum_build.group(1)


def documented_dependency_surface(repo_root: Path) -> dict[str, str]:
    """Parse the identifier table in the dependencies document.

    The table is delimited by its heading and the next section, so package,
    glibc and injection tables elsewhere in the document cannot leak into the
    comparison. Each row maps the first column (backticks optional) to the last
    column, which holds the minimum or fix.
    """

    text = _read(repo_root, "docs/dependencies.md")
    try:
        section = text.split("## What the installers check", 1)[1]
    except IndexError as exc:
        raise ValueError("docs/dependencies.md has no '## What the installers check' section") from exc
    section = section.split("## Linux packages", 1)[0]

    rows: dict[str, str] = {}
    for line in section.splitlines():
        line = line.strip()
        if not line.startswith("|"):
            continue
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        if len(cells) < 4:
            continue
        identifier = cells[0].strip("`").strip()
        if not identifier or identifier == "Identifier" or set(identifier) <= set("-: "):
            continue
        rows[identifier] = cells[-1]
    if not rows:
        raise ValueError("no identifiers parsed from docs/dependencies.md")
    return rows


def drift_problems(repo_root: Path) -> list[str]:
    """Return every mismatch between the installers and the document."""

    linux_ids, windows_ids, floors, minimum_build = installer_dependency_surface(repo_root)
    documented = documented_dependency_surface(repo_root)
    problems: list[str] = []

    installer_ids = linux_ids | windows_ids
    missing = sorted(installer_ids - documented.keys())
    extra = sorted(documented.keys() - installer_ids)
    if missing:
        problems.append(f"installer identifiers missing from docs/dependencies.md: {missing}")
    if extra:
        problems.append(f"documented identifiers no installer checks: {extra}")

    if floors.keys() != {"client", "server"}:
        problems.append(f"install.sh declares unexpected glibc floors: {sorted(floors)}")
    else:
        for component, identifier in (("client", "glibc-client"), ("server", "glibc-server")):
            floor = floors[component]
            if identifier not in documented:
                problems.append(f"docs/dependencies.md does not document {identifier}")
            elif f"`{floor}`" not in documented[identifier]:
                problems.append(
                    f"{identifier} documents {documented[identifier]!r}, "
                    f"but install.sh checks {floor!r}"
                )

    if "windows-version" not in documented:
        problems.append("docs/dependencies.md does not document windows-version")
    elif f"`{minimum_build}`" not in documented["windows-version"]:
        problems.append(
            "docs/dependencies.md does not document the Windows build floor "
            f"{minimum_build} that install.ps1 checks"
        )

    return problems


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[1],
        help="repository root (default: the parent of this script)",
    )
    args = parser.parse_args(argv)

    try:
        problems = drift_problems(args.repo_root)
    except (OSError, ValueError) as exc:
        print(f"dependency drift: {exc}", file=sys.stderr)
        return 1

    for problem in problems:
        print(f"dependency drift: {problem}", file=sys.stderr)
    if problems:
        return 1
    print("installer checks and docs/dependencies.md agree")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
