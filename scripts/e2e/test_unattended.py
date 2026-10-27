import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import unattended


class ReceiptTests(unittest.TestCase):
    def test_interrupt_never_writes_a_passing_receipt(self):
        with tempfile.TemporaryDirectory() as folder:
            report = Path(folder) / "receipt.json"
            argv = ["unattended.py", "--home", folder,
                    "--pc-dir", r"C:\test\e2e3-123", "--agent-task", "DeskhandE2E-123",
                    "--relay-pid", "2", "--report", str(report)]
            with patch.object(sys, "argv", argv), patch.object(unattended.Soak, "run", side_effect=KeyboardInterrupt), contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(KeyboardInterrupt):
                    unattended.main()
            receipt = json.loads(report.read_text())
            self.assertFalse(receipt["passed"])
            self.assertIsNotNone(receipt["error"])

    def test_mcp_error_is_rejected_even_with_python_optimization(self):
        source = """
import sys
from types import SimpleNamespace
sys.path.insert(0, sys.argv[1])
import unattended
soak = unattended.Soak(SimpleNamespace(home='/tmp', pc_dir=r'C:\\test\\e2e3-123'))
soak.command = lambda args: '{"tool":"bad", "is_error":true}\\n'
soak.mcp('bad')
"""
        result = subprocess.run([sys.executable, "-O", "-c", source, str(Path(__file__).parent)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("AssertionError", result.stderr)


if __name__ == "__main__":
    unittest.main()
