#!/usr/bin/env python3
"""Bundle only validated copies of the RDP engine and non-system dependencies."""
import argparse
import hashlib
from pathlib import Path
import subprocess


def run(*args):
    return subprocess.check_output(args, text=True)


def install_ids(snapshot):
    return [line.strip() for line in run('otool', '-D', str(snapshot)).splitlines()[1:] if line.strip()]


def dependencies(snapshot):
    # MH_BUNDLE providers have no LC_ID_DYLIB: their first line is a real dependency.
    ids = install_ids(snapshot)
    return [value for line in run('otool', '-L', str(snapshot)).splitlines()[1:]
            if (value := line.strip().split(' (', 1)[0]) not in ids]


def rpaths(snapshot):
    commands = run('otool', '-l', str(snapshot)).splitlines()
    return [commands[i + 2].strip().split('path ', 1)[1].split(' (offset', 1)[0]
            for i, command in enumerate(commands) if command.strip() == 'cmd LC_RPATH']


def system(path):
    return path.startswith(('/usr/lib/', '/System/Library/'))


def bundle(engine: Path, destination: Path, provider: Path | None = None):
    destination = destination.resolve()
    destination.mkdir(parents=True, exist_ok=True)
    digests = {}
    sign_order = []

    def copy(source):
        source = source.resolve(strict=True)
        # Read once; copy and inspect exactly these bytes even if the build replaces the source.
        data = source.read_bytes()
        digest = hashlib.sha256(data).digest()
        name = source.name
        if name in digests:
            if digests[name] != digest:
                raise RuntimeError(f'Conflicting or changing RDP dependency: {name}')
            return name
        output = destination / name
        if output.is_symlink():
            raise RuntimeError(f'Refusing dependency output symlink: {output}')
        output.write_bytes(data)
        output.chmod(0o755)
        digests[name] = digest
        paths = rpaths(output)
        for dependency in dependencies(output):
            if system(dependency): continue
            if dependency.startswith('@loader_path/'):
                target = source.parent / dependency.removeprefix('@loader_path/')
            elif dependency.startswith('@rpath/'):
                roots = [source.parent]
                for value in paths:
                    if value.startswith('@loader_path'):
                        value = str(source.parent) + value[len('@loader_path'):]
                    if value.startswith('/'):
                        roots.append(Path(value))
                candidates = [root / dependency.removeprefix('@rpath/') for root in roots]
                target = next((path for path in candidates if path.is_file()), None)
                if target is None: raise RuntimeError(f'Unresolved dependency {dependency} of {source}')
            elif dependency.startswith('/'):
                target = Path(dependency)
            else:
                raise RuntimeError(f'Unexpected dependency {dependency} of {source}')
            child = copy(target)
            subprocess.run(['install_name_tool', '-change', dependency, f'@loader_path/{child}', str(output)], check=True)
        if install_ids(output):
            subprocess.run(['install_name_tool', '-id', f'@rpath/{name}', str(output)], check=True)
        for value in paths:
            subprocess.run(['install_name_tool', '-delete_rpath', value, str(output)], check=True)
        sign_order.append(output)
        return name

    name = copy(engine)
    if provider is not None:
        copy(provider)  # OpenSSL loads this module dynamically; otool cannot discover it.
    # install_name_tool can silently ignore unmatched -change targets. Verify every result.
    for output in sign_order:
        for dependency in dependencies(output):
            if system(dependency): continue
            if not dependency.startswith('@loader_path/'):
                raise RuntimeError(f'External dependency survived rewriting: {dependency}')
            target = output.parent / dependency.removeprefix('@loader_path/')
            if target.is_symlink() or target.resolve().parent != destination or target.name not in digests or not target.is_file():
                raise RuntimeError(f'Unbundled dependency: {dependency}')
        if rpaths(output): raise RuntimeError(f'RDP library retains external search paths: {output.name}')
    for output in sign_order:
        subprocess.run(['codesign', '--force', '--sign', '-', str(output)], check=True)
    return name


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('engine', type=Path)
    parser.add_argument('destination', type=Path)
    parser.add_argument('--provider', type=Path, help='OpenSSL legacy module required by NTLM')
    args = parser.parse_args()
    print(bundle(args.engine, args.destination, args.provider))
