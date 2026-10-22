#!/usr/bin/env python3
"""Verify the experimental package without changing any system trust store.

Requires caller-supplied OpenSSL and osslsigncode (https://github.com/mtrojnar/osslsigncode).
Checks DLL/CAT signatures and DLL catalog membership. INF is checked against the
build manifest; this does not claim Windows driver-loading or INF membership proof.
"""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("package", type=Path)
    parser.add_argument("--osslsigncode", required=True)
    parser.add_argument("--openssl", default="openssl")
    args = parser.parse_args()
    root = args.package.resolve()
    manifest_path = root / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8-sig"))
    expected = {f"DeskhandVirtualDisplay.{ext}" for ext in ("dll", "inf", "cat", "cer")}
    entries = manifest["files"]
    if not manifest.get("experimental") or len(entries) != 4 or {e["name"] for e in entries} != expected:
        raise ValueError("Unexpected experimental package manifest")
    files = {}
    for name in expected:
        matches = [p for p in root.iterdir() if p.name.casefold() == name.casefold()]
        if len(matches) != 1 or not matches[0].is_file() or matches[0].is_symlink():
            raise ValueError(f"Missing or ambiguous package file: {name}")
        files[name] = matches[0]
    # Reverification failures revoke a previous success marker.
    manifest["verification"] = "pending"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    for entry in entries:
        actual = hashlib.sha256(files[entry["name"]].read_bytes()).hexdigest()
        if actual.upper() != entry["sha256"].upper():
            raise ValueError(f"Package hash mismatch: {entry['name']}")
    cert = files["DeskhandVirtualDisplay.cer"]
    fingerprint = subprocess.check_output(
        [args.openssl, "x509", "-inform", "DER", "-in", str(cert), "-noout", "-fingerprint", "-sha1"],
        text=True,
    ).strip().split("=", 1)[1].replace(":", "")
    if fingerprint.upper() != manifest["certificate_thumbprint"].upper():
        raise ValueError("Signing certificate identity mismatch")
    with tempfile.TemporaryDirectory(prefix="deskhand-signature-") as temp:
        pem = Path(temp) / "test-root.pem"
        subprocess.run([args.openssl, "x509", "-inform", "DER", "-in", str(cert), "-out", str(pem)], check=True)
        dll, cat = files["DeskhandVirtualDisplay.dll"], files["DeskhandVirtualDisplay.cat"]
        for options in (["-in", str(dll)], ["-in", str(cat)], ["-catalog", str(cat), "-in", str(dll)]):
            subprocess.run([args.osslsigncode, "verify", "-CAfile", str(pem), *options], check=True)
    manifest["verification"] = "offline-signatures-verified"
    manifest["verification_scope"] = ["dll-signature", "cat-signature", "dll-catalog-membership", "manifest-sha256"]
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    print("Offline package signatures verified; Windows loading remains untested.")


if __name__ == "__main__":
    main()
