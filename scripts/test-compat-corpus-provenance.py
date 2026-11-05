"""Bind recorder provenance to the actual binary's VCS metadata and bytes."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('corpus_refresh', Path(__file__).parent / 'compat-corpus/refresh.py')
refresh = importlib.util.module_from_spec(spec)
spec.loader.exec_module(refresh)

class BuildProvenance(unittest.TestCase):
    def test_embedded_clean_revision_and_actual_bytes_are_required(self):
        pin = 'a' * 40
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / 'server'
            binary.write_bytes(b'fixture binary')
            def metadata(revision=pin, modified='false'):
                return json.dumps({'Settings': [{'Key': 'CGO_CFLAGS'}, {'Key': 'vcs.revision', 'Value': revision},
                                                 {'Key': 'vcs.modified', 'Value': modified}]})
            with patch.object(refresh.subprocess, 'check_output', return_value=metadata()) as run:
                result = refresh.recording_build(binary, pin)
                run.assert_called_once_with(['go', 'version', '-m', '-json', str(binary)], text=True)
                self.assertEqual(result, {'sourceRevision': pin, 'modified': False,
                                          'binaryHash': hashlib.sha256(b'fixture binary').hexdigest()})
            for output in [metadata('b' * 40), metadata(modified='true'), '{}']:
                with patch.object(refresh.subprocess, 'check_output', return_value=output), self.assertRaises(RuntimeError):
                    refresh.recording_build(binary, pin)

if __name__ == '__main__':
    unittest.main()
