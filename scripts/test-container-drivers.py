#!/usr/bin/env python3
"""Build and load the exact runtime-driver stage without compiling the product."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import uuid

ROOT = Path(__file__).resolve().parents[1]

# Loading userspace libraries is not evidence of a physical device or codec.
PROBE = r'''
import ctypes, json, pathlib, subprocess
required = ['libva2', 'libva-drm2', 'mesa-va-drivers', 'mesa-vulkan-drivers',
            'mesa-opencl-icd', 'libvulkan1', 'ocl-icd-libopencl1',
            'intel-media-va-driver-non-free', 'i965-va-driver', 'libvpl2',
            'libmfx1', 'libmfx-gen1.2']
lock = json.loads(pathlib.Path('/usr/share/doc/portico/intel-drivers.lock.json').read_text())
distributionPackages = list(required)
for item in lock['packages']:
    # The checksum-pinned archive filename has an exact Debian package name.
    required.append(item['name'].split('_', 1)[0])
required = sorted(set(required))
for package in required:
    status = subprocess.check_output(['dpkg-query', '-W', '-f=${Status}', package], text=True)
    assert status == 'install ok installed', (package, status)
for package in distributionPackages:
    if package != 'libmfx1':
        assert (pathlib.Path('/usr/share/doc') / package / 'copyright').stat().st_size > 0, package
notices = pathlib.Path('/usr/share/doc/portico/licenses/runtime-drivers')
assert (notices / 'intel-media-va-driver-non-free.txt').stat().st_size > 0
for item in lock['packages']:
    package = item['name'].split('_', 1)[0]
    assert any(p.is_file() and p.stat().st_size > 0 for p in (notices / package).rglob('*')), package
sources = pathlib.Path('/etc/apt/sources.list.d/debian.sources').read_text()
assert 'Components: main non-free\n' in sources
assert 'contrib' not in sources and 'non-free-firmware' not in sources
for name in ['libva.so.2', 'libva-drm.so.2', 'libvulkan.so.1', 'libOpenCL.so.1',
             'libvpl.so.2', 'libmfx.so.1', 'libmfxhw64.so.1', 'libmfx-gen.so.1.2']:
    ctypes.CDLL(name)
icds = list(pathlib.Path('/etc/OpenCL/vendors').glob('*.icd'))
assert len(icds) >= 3, [str(i) for i in icds]
for icd in icds:
    for name in icd.read_text().splitlines():
        if name.strip() and not name.lstrip().startswith('#'):
            ctypes.CDLL(name.strip())
for icd in pathlib.Path('/usr/share/vulkan/icd.d').glob('*.json'):
    library = json.loads(icd.read_text())['ICD']['library_path']
    ctypes.CDLL(library)
for package in ['intel-media-va-driver-non-free', 'i965-va-driver', 'mesa-va-drivers']:
    paths = subprocess.check_output(['dpkg-query', '-L', package], text=True).splitlines()
    drivers = [p for p in paths if p.endswith('_drv_video.so')]
    assert drivers, package
    for driver in drivers:
        ctypes.CDLL(driver)
print(json.dumps({'installedPackages': len(required), 'openclIcds': len(icds),
                  'userspaceLibrariesLoad': True, 'physicalGpuTested': False}))
'''

GPU_PROBE = r'''
import hashlib, json, pathlib, subprocess
for name, expected in {'ffmpeg':'e45b768839cb9d169196f6b94845b334b7bfc439e6cf6b301efe769c5f3b5b2e',
                       'ffprobe':'b83289e34eda99fbc3950f2765819b915befbcc9e7fb8e4c352a06e977a2158d'}.items():
    with (pathlib.Path('/tools') / name).open('rb') as f:
        assert hashlib.file_digest(f, 'sha256').hexdigest() == expected, name
results = {}
for backend, init, filters, encoder in [
    ('vaapi', ['-init_hw_device','vaapi=hw:/dev/dri/renderD128'], 'format=nv12,hwupload,scale_vaapi=w=128:h=96', 'h264_vaapi'),
    ('qsv', ['-init_hw_device','vaapi=va:/dev/dri/renderD128','-init_hw_device','qsv=hw@va'], 'format=nv12,hwupload=extra_hw_frames=32,vpp_qsv=w=128:h=96', 'h264_qsv')]:
    output = '/tmp/' + backend + '.mp4'
    args = ['/tools/ffmpeg','-v','error','-nostdin','-y',*init,'-filter_hw_device','hw',
            '-f','lavfi','-i','testsrc2=size=160x128:rate=2','-vf',filters,
            '-frames:v','4','-threads','1','-c:v',encoder,'-b:v','500k',output]
    encoded = subprocess.run(args, capture_output=True, text=True, timeout=30)
    result = {'encoded':encoded.returncode == 0, 'detail':encoded.stderr[-4000:]}
    if encoded.returncode == 0:
        probe = subprocess.run(['/tools/ffprobe','-v','error','-count_frames','-show_streams','-of','json',output],capture_output=True,text=True,timeout=15,check=True)
        video = json.loads(probe.stdout)['streams'][0]
        assert video['codec_name'] == 'h264' and video['width'] == 128 and video['height'] == 96 and int(video['nb_read_frames']) == 4, video
        subprocess.run(['/tools/ffmpeg','-v','error','-nostdin','-i',output,'-threads','1','-f','null','-'],check=True,timeout=15)
        result.update({'decodedFrames':4,'width':128,'height':96})
    pathlib.Path(output).unlink(missing_ok=True)
    results[backend] = result
print(json.dumps({'physicalDevice': '/dev/dri/renderD128', 'deviceSmoke':results,
                  'scope':'Direct container H264 encode/scale plus software decode only; no server sandbox, HDR or other-codec acceptance.'}))
assert any(result['encoded'] for result in results.values()), 'No physical encoder passed'
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--gpu-tools', type=Path, help='exact retained f3 pair directory for an optional physical Intel smoke')
    parser.add_argument('--gpu-test-binary', type=Path, help='same-job decoder test executable for the actual production probe gate')
    args = parser.parse_args()
    if args.gpu_test_binary and not args.gpu_tools:
        raise ValueError('The production GPU probe gate requires the exact f3 tools')
    stage = re.search(r'(?ms)^FROM [^\n]+ AS runtime-drivers\n.*?(?=^FROM runtime-drivers\n)',
                      (ROOT / 'Dockerfile').read_text())
    if not stage:
        raise ValueError('runtime-driver stage missing')
    name = 'portico-hardware-drivers-' + uuid.uuid4().hex[:12]
    label = 'portico.test=' + name
    image = name + ':test'
    with tempfile.TemporaryDirectory(prefix='drivers-', dir=os.environ['TMPDIR']) as temporary:
        context = Path(temporary)
        for relative in ['scripts/install-intel-opencl.py', 'third_party/ffmpeg/intel-drivers.lock.json']:
            destination = context / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / relative, destination)
        lines = stage.group().splitlines()
        lines.insert(1, 'LABEL portico.lane=hardware-r29 ' + label)
        (context / 'Dockerfile').write_text('\n'.join(lines) + '\n')
        try:
            environment = dict(os.environ, DOCKER_BUILDKIT='0')
            subprocess.run(['docker', 'build', '--rm', '--force-rm', '--memory', '1536m',
                            '--memory-swap', '1536m', '--cpu-period', '100000', '--cpu-quota', '200000',
                            '--build-arg', 'TARGETARCH=amd64', '-t', image, str(context)],
                           env=environment, check=True, timeout=1200)
            subprocess.run(['docker', 'run', '--rm', '--name', name, '--label', label,
                            '--label', 'portico.lane=hardware-r29', '--network', 'none',
                            '--cpus', '1', '--memory', '512m', '--memory-swap', '512m',
                            image, 'python3', '-c', PROBE], check=True, timeout=90)
            if args.gpu_tools:
                tools = args.gpu_tools.resolve(strict=True)
                for tool, expected in {'ffmpeg':'e45b768839cb9d169196f6b94845b334b7bfc439e6cf6b301efe769c5f3b5b2e',
                                       'ffprobe':'b83289e34eda99fbc3950f2765819b915befbcc9e7fb8e4c352a06e977a2158d'}.items():
                    path = tools / tool
                    if path.is_symlink() or not path.is_file():
                        raise ValueError('GPU smoke needs the exact regular retained f3 pair')
                    with path.open('rb') as source:
                        if hashlib.file_digest(source, 'sha256').hexdigest() != expected:
                            raise ValueError('GPU smoke pair differs from qualified f3: ' + tool)
                command = ['docker','run','--rm','--name',name,'--label',label,
                           '--label','portico.lane=hardware-r29','--network','none',
                           '--cpus','2','--memory','1g','--memory-swap','1g',
                           '--device','/dev/dri:/dev/dri','-v',str(tools)+':/tools:ro']
                if args.gpu_test_binary:
                    binary = args.gpu_test_binary.resolve(strict=True)
                    if args.gpu_test_binary.is_symlink() or not binary.is_file():
                        raise ValueError('Use the same-job regular decoder test binary')
                    for backend in ['vaapi', 'qsv']:
                        subprocess.run(command + ['-v',str(binary)+':/decoder.test:ro',
                            '-e','PORTICO_TEST_HARDWARE_FFMPEG=/tools/ffmpeg',
                            '-e','PORTICO_TEST_HARDWARE_BACKEND='+backend,
                            image,'/decoder.test','-test.v','-test.run','^TestHardwareDecodeTargetRuntime$'],check=True,timeout=150)
                else:
                    subprocess.run(command + [image,'python3','-c',GPU_PROBE],check=True,timeout=120)
        finally:
            containers = subprocess.check_output(['docker', 'ps', '-aq', '--filter', 'label=' + label], text=True).split()
            if containers:
                subprocess.run(['docker', 'rm', '-f', *containers], check=True)
            images = subprocess.check_output(['docker', 'images', '-aq', '--filter', 'label=' + label], text=True).split()
            if images:
                subprocess.run(['docker', 'rmi', '--no-prune', '-f', *dict.fromkeys(images)], check=True)


if __name__ == '__main__':
    main()
