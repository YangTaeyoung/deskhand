import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('bundle_rdp', Path(__file__).parents[1] / 'bundle-rdp.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class BundleTests(unittest.TestCase):
    def test_inspects_copied_snapshot_when_source_changes(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            source = root / 'source/libEngine.dylib'
            source.parent.mkdir()
            source.write_bytes(b'original-engine')
            dest = root / 'bundle'
            def inspect(*args):
                inspected = Path(args[-1])
                self.assertEqual(inspected.parent, dest.resolve())
                self.assertEqual(inspected.read_bytes(), b'original-engine')
                source.write_bytes(b'concurrently-replaced')
                if args[1] == '-L':
                    return f'{inspected}:\n\t@rpath/libEngine.dylib (compatibility version 1)\n\t/usr/lib/libSystem.B.dylib (compatibility version 1)\n'
                return ''
            with patch.object(module, 'run', side_effect=inspect), patch.object(module.subprocess, 'run'):
                self.assertEqual(module.bundle(source, dest), 'libEngine.dylib')

    def test_unrewritten_external_dependency_is_rejected_before_signing(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            source = root / 'source/libEngine.dylib'; source.parent.mkdir(); source.write_bytes(b'engine')
            child = source.parent / 'libChild.dylib'; child.write_bytes(b'child')
            dest = root / 'bundle'
            def inspect(*args):
                path = Path(args[-1])
                if args[1] == '-l': return ''
                dependency = f'\t{child} (compatibility version 1)\n' if path.name == source.name else '\t/usr/lib/libSystem.B.dylib (compatibility version 1)\n'
                return f'{path}:\n\t@rpath/{path.name} (compatibility version 1)\n' + dependency
            with patch.object(module, 'run', side_effect=inspect), patch.object(module.subprocess, 'run') as command:
                with self.assertRaises(RuntimeError): module.bundle(source, dest)
                self.assertFalse(any(call.args[0][0] == 'codesign' for call in command.call_args_list))


if __name__ == '__main__': unittest.main()
