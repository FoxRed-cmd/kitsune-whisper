"""Release wiring for the detached checksums signature.

These tests run on any host without the release private key. They check the
committed public key, the shape of the release job, and the signing helper
against an ephemeral P-256 key generated in the test.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest
import yaml
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.serialization import load_pem_public_key

REPO_ROOT = Path(__file__).resolve().parents[2]
PUBLIC_KEY = REPO_ROOT / "packaging" / "keys" / "kitsune-release-public.pem"
RELEASE_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "release.yml"
SIGN_SCRIPT = REPO_ROOT / "scripts" / "sign-release-checksums.sh"
DOCS = REPO_ROOT / "docs" / "release.md"


def release_steps() -> list[dict[str, Any]]:
    workflow = yaml.safe_load(RELEASE_WORKFLOW.read_text(encoding="utf-8"))
    steps: list[dict[str, Any]] = workflow["jobs"]["release"]["steps"]
    return steps


def step_index(steps: list[dict[str, Any]], predicate: Any, description: str) -> int:
    for index, step in enumerate(steps):
        if predicate(step):
            return index
    raise AssertionError(f"release job has no {description} step")


def run_openssl(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["openssl", *args], capture_output=True, text=True, timeout=60)


@pytest.fixture
def ephemeral_key(tmp_path: Path) -> tuple[Path, Path]:
    """A throwaway P-256 keypair; the release key is never available to tests."""
    if shutil.which("openssl") is None:
        pytest.skip("openssl is not available")
    private_key = tmp_path / "ephemeral-private.pem"
    public_key = tmp_path / "ephemeral-public.pem"
    generated = run_openssl(
        "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", str(private_key)
    )
    assert generated.returncode == 0, generated.stderr
    exported = run_openssl("pkey", "-in", str(private_key), "-pubout", "-out", str(public_key))
    assert exported.returncode == 0, exported.stderr
    return private_key, public_key


def test_the_committed_public_key_is_an_ec_p256_spki_pem() -> None:
    data = PUBLIC_KEY.read_bytes()
    assert data.startswith(b"-----BEGIN PUBLIC KEY-----")

    key = load_pem_public_key(data)
    assert isinstance(key, ec.EllipticCurvePublicKey)
    assert isinstance(key.curve, ec.SECP256R1)

    if shutil.which("openssl") is not None:
        described = run_openssl("pkey", "-pubin", "-in", str(PUBLIC_KEY), "-text", "-noout")
        assert described.returncode == 0, described.stderr
        assert "NIST CURVE: P-256" in described.stdout


def test_no_private_key_ships_in_the_repository() -> None:
    for path in (REPO_ROOT / "packaging" / "keys").iterdir():
        assert b"PRIVATE KEY" not in path.read_bytes(), path
    # The release job reads the key from a secret, never a committed file.
    workflow = RELEASE_WORKFLOW.read_text(encoding="utf-8")
    assert "PRIVATE KEY" not in workflow


def test_the_release_job_signs_the_checksums_before_publishing() -> None:
    steps = release_steps()
    checksums = step_index(steps, lambda step: step.get("name") == "Generate checksums", "checksum")
    signing = step_index(steps, lambda step: step.get("name") == "Sign the checksums", "signing")
    publish = step_index(
        steps,
        lambda step: str(step.get("uses", "")).startswith("softprops/action-gh-release"),
        "publish",
    )
    assert checksums < signing < publish

    step = steps[signing]
    assert step["env"]["RELEASE_SIGNING_KEY"] == "${{ secrets.RELEASE_SIGNING_KEY }}"
    script = step["run"]
    # A missing secret is a hard failure, not an unsigned release.
    assert 'if [[ -z "${RELEASE_SIGNING_KEY:-}" ]]' in script
    assert "refusing to publish unsigned artifacts" in script
    assert "exit 1" in script
    # The key lives in a private temporary file, removed on every exit path.
    assert "umask 077" in script
    assert "mktemp" in script
    assert "trap 'rm -f \"$key_file\"' EXIT" in script
    assert 'rm -f "$key_file"' in script
    assert "scripts/sign-release-checksums.sh" in script
    assert "dist/checksums.txt" in script
    # Never print the secret.
    assert 'echo "$RELEASE_SIGNING_KEY"' not in script
    assert 'cat "$key_file"' not in script


def test_the_release_publishes_both_the_checksums_and_the_signature() -> None:
    steps = release_steps()
    publish = steps[
        step_index(
            steps,
            lambda step: str(step.get("uses", "")).startswith("softprops/action-gh-release"),
            "publish",
        )
    ]
    files = [line.strip() for line in publish["with"]["files"].splitlines() if line.strip()]
    assert "dist/checksums.txt" in files
    assert "dist/checksums.txt.sig" in files


def test_the_signing_script_writes_a_verifiable_detached_der_signature(
    ephemeral_key: tuple[Path, Path], tmp_path: Path
) -> None:
    private_key, public_key = ephemeral_key
    checksums = tmp_path / "checksums.txt"
    checksums.write_text("abc123  kitsune-client_linux_amd64.tar.gz\n", encoding="utf-8")

    signed = subprocess.run(
        [
            str(SIGN_SCRIPT),
            "--key",
            str(private_key),
            "--checksums",
            str(checksums),
            "--public-key",
            str(public_key),
        ],
        capture_output=True,
        text=True,
        timeout=60,
    )

    assert signed.returncode == 0, signed.stderr
    signature = tmp_path / "checksums.txt.sig"
    assert signature.is_file()
    # OpenSSL emits an ASN.1 DER SEQUENCE; .NET needs Rfc3279DerSequence.
    assert signature.read_bytes()[0] == 0x30

    verified = run_openssl(
        "dgst",
        "-sha256",
        "-verify",
        str(public_key),
        "-signature",
        str(signature),
        str(checksums),
    )
    assert verified.returncode == 0, verified.stderr
    assert "Verified OK" in verified.stdout


def test_a_changed_checksum_file_no_longer_verifies(
    ephemeral_key: tuple[Path, Path], tmp_path: Path
) -> None:
    private_key, public_key = ephemeral_key
    checksums = tmp_path / "checksums.txt"
    checksums.write_text("abc123  kitsune-client_linux_amd64.tar.gz\n", encoding="utf-8")
    assert (
        subprocess.run(
            [
                str(SIGN_SCRIPT),
                "--key",
                str(private_key),
                "--checksums",
                str(checksums),
                "--public-key",
                str(public_key),
            ],
            capture_output=True,
            text=True,
            timeout=60,
        ).returncode
        == 0
    )

    checksums.write_text("def456  kitsune-client_linux_amd64.tar.gz\n", encoding="utf-8")
    verified = run_openssl(
        "dgst",
        "-sha256",
        "-verify",
        str(public_key),
        "-signature",
        str(tmp_path / "checksums.txt.sig"),
        str(checksums),
    )
    assert verified.returncode != 0


def test_a_secret_for_the_wrong_key_fails_before_the_signature_is_written(
    ephemeral_key: tuple[Path, Path], tmp_path: Path
) -> None:
    private_key, _ = ephemeral_key
    checksums = tmp_path / "checksums.txt"
    checksums.write_text("abc123  kitsune-client_linux_amd64.tar.gz\n", encoding="utf-8")

    # Sign with a temporary key but verify against the committed release key.
    signed = subprocess.run(
        [
            str(SIGN_SCRIPT),
            "--key",
            str(private_key),
            "--checksums",
            str(checksums),
            "--public-key",
            str(PUBLIC_KEY),
        ],
        capture_output=True,
        text=True,
        timeout=60,
    )

    assert signed.returncode != 0
    assert "does not verify" in signed.stderr
    assert not (tmp_path / "checksums.txt.sig").exists()


def test_the_documentation_shows_both_verification_paths() -> None:
    docs = DOCS.read_text(encoding="utf-8")
    assert "packaging/keys/kitsune-release-public.pem" in docs
    assert "openssl dgst -sha256 -verify" in docs
    assert "DSASignatureFormat.Rfc3279DerSequence" in docs
    assert "ImportFromPem" in docs
    assert "RELEASE_SIGNING_KEY" in docs
    assert "Losing the key" in docs
