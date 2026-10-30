#!/usr/bin/env python3
import hashlib,json,pathlib,re,sys
root=pathlib.Path(sys.argv[1]).resolve();m=json.loads((root/'toolchain-manifest.json').read_text())
if m.get('schemaVersion')!=1 or m.get('licenseMode')!='gpl3':raise SystemExit('Invalid manifest')
files=m['files']
actual={str(p.relative_to(root)) for p in root.rglob('*') if p.is_file() and p.name!='toolchain-manifest.json'}
if actual!=set(files):raise SystemExit('Bundle file inventory mismatch')
def digest_file(path):
 with path.open('rb') as stream:return hashlib.file_digest(stream,'sha256').hexdigest()
for required in ['corresponding-source.tar.xz','NOTICE.md','sources.lock.json','requirements.v1.json','portico-dovi-profile.v1.json','portico-disc-build.json','LICENSES/portico-disc.txt']:
 if required not in files:raise SystemExit('Missing provenance: '+required)
if m['target']=='linux-x64':
 for required in ['dependency-inputs.json','compiler.txt','compiler-linked-inputs.json','license-inventory.json','linux-build-inputs.json','repeat-build.json','libdovi-build.json','dovi-profile-build.json','dovi-video-build.json','portico-dovi-join.v1.json','linux-hdr-gate.v1.json']:
  if required not in files:raise SystemExit('Missing Linux build provenance: '+required)
if not any(x.startswith('LICENSES/') for x in files):raise SystemExit('Missing licenses')
for name,digest in files.items():
 path=root/name
 if not path.resolve().is_relative_to(root) or path.is_symlink() or not path.is_file() or digest_file(path)!=digest:raise SystemExit('Bundle verification failed: '+name)
for tool in ['ffmpeg','ffprobe','portico-disc']:
 name='bin/'+tool+('.exe' if m['target'].startswith('windows-') else '')
 if name not in files:raise SystemExit('Missing executable: '+name)
if m['target']=='linux-x64':
 rounds=json.loads((root/'repeat-build.json').read_text())
 inputs=json.loads((root/'linux-build-inputs.json').read_text())
 expected={tool:files['bin/'+tool] for tool in ['ffmpeg','ffprobe','portico-disc']}
 if (len(rounds)!=2 or [r.get('round') for r in rounds]!=[1,2] or
     not all(r.get('binaries')==expected and r.get('sourceArchiveSHA256')==inputs.get('sourceArchiveSHA256') for r in rounds)):
  raise SystemExit('Repeated executable builds do not match this bundle')
 if not all(isinstance(inputs.get(key),str) and re.fullmatch(r'[0-9a-f]{64}',inputs[key]) for key in ['sourceArchiveSHA256','dependencyPrefixFilesSHA256']):
  raise SystemExit('Missing sealed Linux source/dependency identity')
 if not re.fullmatch(r'[0-9a-f]{64}',str(inputs.get('nativeBuildTools',{}).get('glslc',''))):
  raise SystemExit('Missing source-built native shader compiler identity')
 profile=json.loads((root/'dovi-profile-build.json').read_text())
 contract=json.loads((root/'portico-dovi-profile.v1.json').read_text())
 expected_packet={'passed':True,'convertedPackets':96,'wrongRpuProfilesRejected':3,'invalidConfigurationsRejected':4,'corruptRpuRejected':1,'noRpuRejected':True,'modeIntrospection':True}
 if (profile.get('qualified') is not True or profile.get('ffmpegSHA256')!=files['bin/ffmpeg'] or
     profile.get('sourceArchiveSHA256')!=inputs['sourceArchiveSHA256'] or
     profile.get('patchSHA256')!=contract['ffmpeg']['patchSha256'] or
     profile.get('packetTestSourceSHA256')!=contract['hostProof']['packetTestSourceSha256'] or
     profile.get('packetTestSourceSHA256')!=inputs['files'].get('dovi-profile-packets.c') or
     profile.get('fixtureManifestSHA256')!=inputs['files'].get('fixtures/manifest.json') or
     not all(profile.get('packetTests',{}).get(k)==v for k,v in expected_packet.items()) or
     not all(all(r.get('packetTests',{}).get(k)==v for k,v in expected_packet.items()) for r in rounds)):
  raise SystemExit('Canonical Dolby Vision packet proof does not match this bundle')
 video=json.loads((root/'dovi-video-build.json').read_text())
 join=json.loads((root/'portico-dovi-join.v1.json').read_text())
 gate=json.loads((root/'linux-hdr-gate.v1.json').read_text())
 if (video.get('qualified') is not True or video.get('binarySHA256')!=expected or
     video.get('sourceArchiveSHA256')!=inputs['sourceArchiveSHA256'] or
     video.get('fixtureManifestSHA256')!=gate['inputs']['fixtureManifest']['sha256'] or
     video.get('joinPatchSHA256')!=join['patchSha256'] or
     video.get('joinFixtureManifestSHA256')!=join['fixtureManifestSha256'] or
     video.get('join',{}).get('passed') is not True or
     video['join'].get('joinedCases')!=join['gate']['positive'] or
     video['join'].get('refusalCases')!=join['gate']['refused'] or
     len(video['join'].get('fixtures',[]))!=join['fixtures'] or
     video.get('hdr10plus',{}).get('passed') is not True or
     video['hdr10plus'].get('frames')!=24 or
     video.get('video',{}).get('passed') is not True):
  raise SystemExit('Canonical encoded HDR or corrected dual-PID proof does not match this bundle')
 disc=json.loads((root/'portico-disc-build.json').read_text())
 lock=json.loads((root/'sources.lock.json').read_text())
 if (disc.get('sourceCommit')!=lock['discHelper']['sourceCommit'] or
     disc.get('sourceFiles')!=lock['discHelper']['sourceFiles'] or
     disc['sourceFiles'].get('portico-disc.c')!=inputs['files'].get('portico-disc.c') or
     disc['sourceFiles'].get('mpegts_dovi.h')!=inputs['files'].get('mpegts_dovi.h')):
  raise SystemExit('Canonical disc C/header source identity differs from this bundle')

print('Verified bundle hashes for '+m['target'])
