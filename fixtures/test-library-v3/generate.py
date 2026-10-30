#!/usr/bin/env python3
"""Build the original, local-only Portico v3 sample media library.

Install requirements.txt in an isolated Python environment. Run from a checkout:
  python generate.py --root /absolute/empty/library --ffmpeg /qualified/bin/ffmpeg
The existing committed fixtures/video/hdr-generated and fixtures/discs inputs
are procedural fixtures. Dolby Vision reuses the committed procedural MEL/FEL
MP4 containers, preserving their actual Dolby Vision configuration record.
No network, commercial media or design assets are read.
All generated media and per-section manifests belong on the test host.
Use seed_state.py on the operator host to populate state through the real API;
credentials remain in the operator's private file. Use verify.py and
playback_smoke.py for readback and the bounded service qualification checks.
"""
import argparse, collections, hashlib, importlib, json, os, shutil
from pathlib import Path


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,required=True)
    parser.add_argument('--ffmpeg',default='/opt/portico-ffmpeg/bin/ffmpeg')
    parser.add_argument('--sections',nargs='+',choices=['video','audio','reading','live'],default=['video','audio','reading','live'])
    parser.add_argument('--small',action='store_true',help='Generate a small developer subset')
    args=parser.parse_args()
    root=args.root.resolve()
    if root == Path('/') or root.is_symlink():
        parser.error('Use a dedicated non-symlink output directory')
    root.mkdir(parents=True,exist_ok=True)
    if shutil.disk_usage(root).free < 8*1024**3:
        parser.error('At least 8 GiB free is required before generation')
    entries=[]
    libraries=[]
    for section in args.sections:
        print('Generating '+section,flush=True)
        items=importlib.import_module(section).generate(root,args.ffmpeg,not args.small)
        out=root/('manifest-'+section+'.json')
        out.write_text(json.dumps(items,ensure_ascii=False,indent=2)+'\n')
    for section in ['video','audio','reading','live']:
        path=root/('manifest-'+section+'.json')
        if path.exists():
            entries.extend(json.loads(path.read_text()))
            module=importlib.import_module(section)
            for lib in getattr(module,'LIBRARIES',[]):
                declaration={'name':'Synthetic '+lib['name'].removeprefix('Design Test ').removeprefix('Portico '), 'kind':lib['kind'], 'path':lib.get('path',lib.get('root'))}
                expected={k:v for k,v in lib.items() if k not in ('name','kind','path','root')}
                if expected: declaration['expected']=expected
                libraries.append(declaration)
    manifest={'schemaVersion':1,'seed':'portico-original-20261027','libraries':libraries,'entries':entries,
              'counts':dict(sorted(collections.Counter(e['kind'] for e in entries).items())),
              'provenance':'Original generated pictures, text, tones and local metadata; no external lookup.'}
    target=root/'manifest.json'
    tmp=target.with_suffix('.json.tmp')
    # One entry per line keeps the committed catalogue reviewable.
    header={k:v for k,v in manifest.items() if k!='entries'}
    payload=json.dumps(header,ensure_ascii=False,indent=2)[:-2]+',\n  "entries": [\n'
    payload+=',\n'.join('    '+json.dumps(e,ensure_ascii=False,separators=(',',':')) for e in entries)
    payload+='\n  ]\n}\n'
    tmp.write_text(payload);tmp.replace(target)
    print(json.dumps(manifest['counts'],sort_keys=True),flush=True)


if __name__=='__main__': main()
