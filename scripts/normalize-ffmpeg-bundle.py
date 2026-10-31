#!/usr/bin/env python3
"""Seal an exact build and corresponding source; never manufacture qualification."""
import argparse,hashlib,json,pathlib,shutil
root=pathlib.Path(__file__).resolve().parent.parent
p=argparse.ArgumentParser();p.add_argument('input',type=pathlib.Path);p.add_argument('target');p.add_argument('output',type=pathlib.Path);p.add_argument('source',type=pathlib.Path);p.add_argument('--working',action='store_true',help='stage a first working bundle without release qualification');a=p.parse_args()
if a.target not in ['linux-x64','linux-arm64','windows-x64','windows-arm64','macos-x64','macos-arm64']:p.error('unknown target')
if a.output.exists():p.error('output must not exist')
if not a.source.is_file() or a.source.stat().st_size<1024:p.error('corresponding source archive is required')
a.output.mkdir(parents=True);(a.output/'bin').mkdir();(a.output/'LICENSES').mkdir()
for tool in ['ffmpeg','ffprobe','portico-disc']:
 name=tool+('.exe' if a.target.startswith('windows-') else '')
 matches=sorted(x for x in a.input.rglob(name) if x.is_file() and not x.is_symlink())
 if len(matches)!=1:p.error('expected one '+name+' executable')
 shutil.copy2(matches[0],a.output/'bin'/name)
disc_metadata=sorted(x for x in a.input.rglob('portico-disc-build.json') if x.is_file() and not x.is_symlink())
if len(disc_metadata)!=1:p.error('expected one disc source/static-library provenance file')
shutil.copy2(disc_metadata[0],a.output/'portico-disc-build.json')
disc_licenses=sorted(x for x in a.input.rglob('portico-disc-LICENSE') if x.is_file() and not x.is_symlink())
if len(disc_licenses)!=1:p.error('expected one checked disc reader license')
shutil.copy2(disc_licenses[0],a.output/'LICENSES/portico-disc.txt')
for name in ['dependency-inputs.json', 'compiler.txt', 'compiler-linked-inputs.json', 'license-inventory.json', 'linux-build-inputs.json', 'repeat-build.json', 'libdovi-build.json', 'dovi-profile-build.json', 'dovi-video-build.json'] + (['source-build-receipt.json'] if a.target.startswith('windows') else []):
 matches=sorted(x for x in a.input.rglob(name) if x.is_file() and not x.is_symlink())
 if a.target=='linux-x64' and len(matches)!=1 and not (a.working and name in ('repeat-build.json','dovi-profile-build.json','dovi-video-build.json')):p.error('expected one '+name+' provenance file')
 if len(matches)==1:shutil.copy2(matches[0],a.output/name)
# Keep the collected source-relative notices intact. Several dependencies use
# COPYRIGHT or NOTICE, and nested names distinguish separate upstream licences.
for f in sorted(a.input.rglob('*')):
 if not f.is_file() or f.is_symlink():continue
 relative=f.relative_to(a.input)
 if 'LICENSES' in relative.parts:
  destination=a.output/relative
 elif f.name.lower().startswith(('license','licence','copying','copyright','notice')):
  destination=a.output/'LICENSES'/'build'/relative
 else:continue
 destination.parent.mkdir(parents=True,exist_ok=True)
 shutil.copy2(f,destination)
if len(list((a.output/'LICENSES').iterdir()))<2:p.error('dependency license texts missing')
shutil.copy2(a.source,a.output/'corresponding-source.tar.xz')
for name in ['NOTICE.md','sources.lock.json','requirements.v1.json','portico-dovi-profile.v1.json','portico-dovi-join.v1.json','linux-hdr-gate.v1.json']:shutil.copy2(root/'third_party/ffmpeg'/name,a.output/name)
def digest(path):
 with path.open('rb') as stream:return hashlib.file_digest(stream,'sha256').hexdigest()
files={str(f.relative_to(a.output)):digest(f) for f in sorted(a.output.rglob('*')) if f.is_file()}
lock=json.loads((a.output/'sources.lock.json').read_text())
# Working output deliberately cannot satisfy the production bundle resolver.
manifest_name='working-manifest.json' if a.working else 'toolchain-manifest.json'
(a.output/manifest_name).write_text(json.dumps({'schemaVersion':1,'target':a.target,'ffmpegVersion':lock['ffmpeg']['version'],'buildId':lock['releaseTag'],'licenseMode':'gpl3','files':files},indent=2)+'\n')
