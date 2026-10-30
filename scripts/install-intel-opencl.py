#!/usr/bin/env python3
"""Install only checksum-pinned official Intel current and legacy OpenCL ICDs."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

lock = json.loads(Path(sys.argv[1]).read_text())
arch = subprocess.check_output(['dpkg', '--print-architecture'], text=True).strip()
if arch != lock['architecture']:
    sys.exit(0)
with tempfile.TemporaryDirectory(prefix='portico-intel-opencl-') as folder:
    packages = []
    package_info = {}
    shared_notices = []
    notices = Path('/usr/share/doc/portico/licenses/runtime-drivers')
    notices.mkdir(parents=True, exist_ok=True)
    for item in lock['packages']:
        path = Path(folder) / item['name']
        urllib.request.urlretrieve(item['url'], path)
        with path.open('rb') as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != item['sha256']:
                raise ValueError('Intel driver checksum mismatch: ' + item['name'])
        packages.append(str(path))
        name = subprocess.check_output(['dpkg-deb', '--field', str(path), 'Package'], text=True).strip()
        version = subprocess.check_output(['dpkg-deb', '--field', str(path), 'Version'], text=True).strip()
        package_info[name] = (version, item['release'], item['sourceRepository'])
        # Debian slim intentionally drops most /usr/share/doc payloads. Retain
        # the exact archive's licence texts before apt applies those filters;
        # upstream IGC uses LICENSE files rather than Debian's copyright name.
        found = 0
        with subprocess.Popen(['dpkg-deb', '--fsys-tarfile', str(path)], stdout=subprocess.PIPE) as process:
            with tarfile.open(fileobj=process.stdout, mode='r|') as contents:
                for member in contents:
                    basename = Path(member.name).name.lower()
                    if not member.isfile() or not any(word in basename for word in ('license', 'licence', 'copyright', 'copying', 'notice')):
                        continue
                    relative = Path(member.name)
                    if relative.is_absolute() or '..' in relative.parts or not 0 < member.size <= 2 << 20:
                        raise ValueError('Invalid driver licence member: ' + member.name)
                    destination = notices / name / relative
                    destination.parent.mkdir(parents=True, exist_ok=True)
                    with contents.extractfile(member) as source, destination.open('xb') as target:
                        target.write(source.read())
                    print('Retained driver licence:', name, member.name, flush=True)
                    found += 1
            if process.wait() != 0:
                raise ValueError('Intel driver archive unreadable: ' + name)
        if not found:
            shared_notices.append((name, item.get('licensePackage')))
    # IGC's OpenCL split package ships its licences in the matching core
    # package. Permit only an explicit lock reference to the exact same
    # version/release/source, never an unrelated system-installed licence.
    for name, core in shared_notices:
        if not core or package_info.get(core) != package_info[name] or not (notices / core).is_dir():
            raise ValueError('Intel driver licence missing from pinned archive: ' + name)
        shutil.copytree(notices / core, notices / name)
    subprocess.run(['apt-get', 'install', '--yes', '--no-install-recommends', *packages], check=True)
