import hashlib,importlib.util,tempfile,unittest
from pathlib import Path
spec=importlib.util.spec_from_file_location('binary_policy',Path(__file__).resolve().parents[1]/'scripts/check-repo-binaries.py')
policy=importlib.util.module_from_spec(spec);spec.loader.exec_module(policy)
class FilePolicyTests(unittest.TestCase):
 def test_oversize_exception_is_exact_and_cannot_allow_an_executable(self):
  with tempfile.TemporaryDirectory() as folder:
   root=Path(folder);p=root/'asset.png';data=b'a'*(policy.LIMIT+1);p.write_bytes(data)
   self.assertIsNotNone(policy.error(root,'asset.png',{}))
   entry={'asset.png':{'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}}
   self.assertIsNone(policy.error(root,'asset.png',entry))
   p.write_bytes(b'b'+data[1:]);self.assertIsNotNone(policy.error(root,'asset.png',entry))
   p.write_bytes(bytes.fromhex('7f454c46')+data[4:]);entry['asset.png']['sha256']=hashlib.sha256(p.read_bytes()).hexdigest()
   self.assertIn('executable',policy.error(root,'asset.png',entry))
