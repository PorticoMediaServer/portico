#!/usr/bin/env python3
"""Build only from an isolated source snapshot; never seal a supplied executable."""
import runpy
import argparse,datetime,hashlib,json,os,pathlib,shutil,subprocess,uuid,shlex,re,base64
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey,Ed25519PublicKey
ROOT=pathlib.Path(__file__).resolve().parent.parent
RCLONE=runpy.run_path(str(ROOT/'scripts/rclone-bundle.py'))
SONIC=runpy.run_path(str(ROOT/'scripts/sonic-bundle.py'))
TAGS=runpy.run_path(str(ROOT/'scripts/release-build-tags.py'))
DEV_ENGINE=runpy.run_path(str(ROOT/'scripts/development-engine.py'))
BUILD_TAGS=TAGS['value']()

def module_path(component):
    return 'github.com/PorticoMediaServer/portico/server' if component=='server' else 'portico.local/'+component

def build_tags(component):
    # The server carries the reader capability; only the server is a buildable
    # component today, and its tag set is the single checked list.
    return BUILD_TAGS if component=='server' else 'release'

def tags_are_official(component, supplied):
    return TAGS['covers'](supplied) if component=='server' else TAGS['tags'](supplied)=={'release'}

def official_root():
    path=pathlib.Path(os.environ.get('PORTICO_ROOT_PUBLIC_KEY_FILE',str(ROOT/'trust/hosted-root-public-key.pem')))
    if not path.is_file():raise SystemExit('Official root public key file required for release')
    raw=path.read_bytes();key=serialization.load_pem_public_key(raw)
    if not isinstance(key,Ed25519PublicKey):raise SystemExit('Official root must be Ed25519')
    encoded=base64.urlsafe_b64encode(key.public_bytes_raw()).decode().rstrip('=')
    if encoded in (ROOT/'server/internal/hostedtrust/development_root.go').read_text():raise SystemExit('Development root is forbidden in release builds')
    return encoded,raw

# Siblings read by mandatory Go tests, plus their checked-in asset inventory.
# Keep their repository paths: the tests intentionally check the actual writer,
# catalogue and runtime consumers rather than substitute fixtures.
TEST_INPUTS = (
    'go.work',
    'scripts/write-public-update-manifest.mjs',
    'scripts/vet-go.sh',
    # vet-go.sh reads the build tags from its own directory's copy.
    'scripts/release-build-tags.py',
    'packages/i18n/src/catalog/en-US/settings.ts',
    'packages/client-core/src/music-preferences.ts',
    'packages/client-core/src/audiobook-preferences.ts',
    'packages/client-core/src/preference-effects.ts',
    'web/src/shell/bootstrap.ts',
    'web/src/shell/settings.ts',
    'third_party/rclone/bundle.lock.json',
)

def input_inventory(source, siblings):
    _,files=digest_source(source)
    dependency=siblings/'apikit'
    if not (dependency/'go.mod').is_file():raise ValueError('Missing release input: apikit/go.mod')
    _,rows=digest_source(dependency)
    files.extend({'path':'../apikit/'+r['path'],'sha256':r['sha256']} for r in rows)
    for name in TEST_INPUTS:
        base=siblings/name
        if not base.exists():raise ValueError('Missing release test input: '+name)
        if base.is_symlink():raise ValueError('Release test input must not be a symlink: '+name)
        for path in sorted(base.rglob('*') if base.is_dir() else [base]):
            if path.is_symlink():raise ValueError('Release test input must not be a symlink: '+str(path))
            if path.is_file():
                files.append({'path':'../'+str(path.relative_to(siblings)),
                              'sha256':hashlib.sha256(path.read_bytes()).hexdigest()})
    return files

def snapshot_digest(source):
    files=input_inventory(source,source.parent)
    public=source.parent/'root-public-key.pem'
    if public.is_file():files.append({'path':'../root-public-key.pem','sha256':hashlib.sha256(public.read_bytes()).hexdigest()})
    return hashlib.sha256(json.dumps(files,separators=(',',':'),sort_keys=True).encode()).hexdigest(),files

def capture_source(live,stage,root_pem):
    source=stage/'server'
    source.mkdir(parents=True)
    before=input_inventory(live,ROOT)
    for entry in before:
        relative=entry['path']
        original=ROOT/relative[3:] if relative.startswith('../') else live/relative
        destination=source/relative
        destination.parent.mkdir(parents=True,exist_ok=True)
        shutil.copyfile(original,destination)
    # Compare the complete input set on both sides of the copy. Concurrent
    # sibling edits must fail capture just like concurrent module edits.
    if input_inventory(source,stage)!=before or input_inventory(live,ROOT)!=before:
        raise ValueError('Source or test dependency changed during capture; retry after edits settle.')
    # Go workspace membership follows the physical module directory.
    (stage/'source').symlink_to(source.name,target_is_directory=True)
    (stage/'root-public-key.pem').write_bytes(root_pem)
    return snapshot_digest(source)

def controlled_env():
    env=os.environ.copy()
    for key in ('GOOS','GOARCH','GOEXPERIMENT','GOROOT','GOTOOLDIR','GOTMPDIR','CC','CXX','CGO_CFLAGS','CGO_CPPFLAGS','CGO_CXXFLAGS','CGO_LDFLAGS'):env.pop(key,None)
    env.update({'GOWORK':'off','GOENV':'off','GOTOOLCHAIN':'local','GOPROXY':'https://proxy.golang.org','GOSUMDB':'sum.golang.org','GOPRIVATE':'','GONOSUMDB':'','GONOPROXY':''})
    return env

def signing_key():
    path=pathlib.Path(os.environ.get('PORTICO_RELEASE_SIGNING_KEY_FILE',str(ROOT/'runtime/release-signing/private-key')))
    if not path.is_file():raise ValueError('Provide the approved release signing key; release builds never create one')
    if path.is_symlink() or path.stat().st_mode&0o077:raise ValueError('Release signing key permissions are unsafe')
    key=Ed25519PrivateKey.from_private_bytes(path.read_bytes())
    expected=os.environ.get('PORTICO_RELEASE_PUBLIC_KEY_FILE')
    if not expected:raise ValueError('Provide the pinned release public key for independent verification')
    if pathlib.Path(expected).read_bytes()!=key.public_key().public_bytes_raw():raise ValueError('Release signer differs from the pinned public key')
    return key

def source_files(source):
    # The whole module tree: Go source, embedded files (migrations, fonts, the
    # Cast receiver), protocol vectors and test data. Filtering by extension
    # left embedded files out, and the snapshot could not compile.
    return sorted(p for p in source.rglob('*') if p.is_file() and p.name!='.DS_Store' and not any(x in ('runtime','bin','vendor','.scratch','node_modules') for x in p.relative_to(source).parts))

def digest_source(source):
    files=[{'path':str(p.relative_to(source)), 'sha256':hashlib.sha256(p.read_bytes()).hexdigest()} for p in source_files(source)]
    digest=hashlib.sha256(json.dumps(files,separators=(',',':'),sort_keys=True).encode()).hexdigest()
    return digest,files

def artifact_identity(artifact):
    info=json.loads(subprocess.check_output([str(ROOT/'scripts/env.sh'),'go','version','-m','-json',str(artifact)],text=True,env=controlled_env()))
    settings={entry['Key']:entry.get('Value','') for entry in info.get('Settings',[])}
    if any(key in settings for key in ('-overlay','-toolexec','-modfile','-pkgdir')):raise ValueError('Unexpected compiler input override')
    args=shlex.split(settings.get('-ldflags',''));embedded={}
    for index,arg in enumerate(args):
        if arg=='-X' and index+1<len(args):
            key,separator,value=args[index+1].partition('=')
            if separator:embedded[key]=value
    # Go (1.20+, golang/go#52372) records no -ldflags for a -trimpath build, so the release identity
    # also travels in the binary as one proof marker (buildinfo.Proof), and is read from there.
    proofs=set(re.findall(rb'PORTICO_RELEASE_V2:([a-f0-9]{64}):([A-Za-z0-9_-]+):([0-9A-Za-z.+-]+):([A-Za-z0-9_-]+):END',artifact.read_bytes()))
    module=info.get('Path','').split('/cmd/')[0]
    if module not in ('github.com/PorticoMediaServer/portico/server','portico.local/hosted'):raise ValueError('Unexpected application module')
    prefix=module+'/internal/buildinfo.'
    trust_symbol='github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot' if module.endswith('/server') else 'portico.local/hosted/trust.OfficialRoot'
    if '-ldflags' not in settings:
        if len(proofs)!=1:raise ValueError('A release binary built without recorded -ldflags must carry exactly one identity proof')
        source,build_id,version,root=(x.decode() for x in next(iter(proofs)))
        # The marker names the trust root; the binary must also carry that exact value outside the
        # marker (the -X string the hostedtrust variable points at), so a marker can't claim a root
        # the server doesn't use.
        if artifact.read_bytes().count(root.encode())<2:raise ValueError('The proof marker names a trust root the binary does not otherwise carry')
        embedded={prefix+'SourceDigest':source,prefix+'BuildID':build_id,prefix+'Version':version,trust_symbol:root}
    expected=tuple(embedded.get(k,'').encode() for k in (prefix+'SourceDigest',prefix+'BuildID',prefix+'Version',trust_symbol))
    if proofs and proofs!={expected}:raise ValueError('Proof marker disagrees with compiler build identity')
    return embedded,settings

def verify_manifest(directory,expected_source,trusted_public_key):
    manifest_bytes=(directory/'manifest.json').read_bytes()
    Ed25519PublicKey.from_public_bytes(trusted_public_key).verify((directory/'manifest.sig').read_bytes(),manifest_bytes)
    manifest=json.loads(manifest_bytes)
    if manifest.get('sealed') is not True or manifest.get('engine',{}).get('development'):raise ValueError('An unsealed build or a development engine never verifies as a release')
    return check_contents(directory,expected_source,manifest)

def check_contents(directory,expected_source,manifest,development=False):
    if manifest['sourceDigest']!=expected_source:raise ValueError('Release source does not match expected source')
    component=manifest.get('component','server')
    if component not in ('server','hosted'):raise ValueError('Invalid release component')
    artifact=directory/manifest['artifact']['path']
    if artifact.parent!=directory or artifact.name!='portico-'+component+('.exe' if manifest['target'].startswith('windows-') else ''):raise ValueError('Invalid artifact path')
    if hashlib.sha256(artifact.read_bytes()).hexdigest()!=manifest['artifact']['sha256']:raise ValueError('Release artifact was replaced or modified')
    source_digest,_=snapshot_digest(directory/'source')
    if source_digest!=expected_source:raise ValueError('Build snapshot changed')
    embedded,settings=artifact_identity(artifact)
    trust_symbol='github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot' if component=='server' else 'portico.local/hosted/trust.OfficialRoot'
    if not tags_are_official(component,settings.get('-tags')) or embedded.get(trust_symbol)!=manifest.get('officialRoot'):raise ValueError('Release lacks the certified official trust root or its official build tags')
    if settings.get('-trimpath')!='true':raise ValueError('Release was built without -trimpath and carries build-machine paths')
    prefix=module_path(component)+'/internal/buildinfo.'
    if embedded.get(prefix+'SourceDigest')!=expected_source or embedded.get(prefix+'BuildID')!=manifest['buildId']:raise ValueError('Embedded build identity does not match release source')
    if manifest.get('version') is not None and embedded.get(prefix+'Version')!=manifest['version']:raise ValueError('Embedded version does not match release manifest')
    if settings.get('GOOS','')+'-'+settings.get('GOARCH','')!=manifest['target']:raise ValueError('Embedded target does not match release target')
    if component=='server':
        bundle=directory/'third_party/ffmpeg'
        if manifest.get('sonicManifestSha256'):
            target='linux-'+{'amd64':'x64','arm64':'arm64'}[manifest['target'].split('-')[1]]
            SONIC['verify_installed'](bundle,target)
            if SONIC['digest'](bundle/'bin/sonic-manifest.json')!=manifest['sonicManifestSha256']:raise ValueError('Sonic manifest changed')
        elif development:
            DEV_ENGINE['verify'](bundle,manifest['target'].replace('darwin-','macos-').replace('-amd64','-x64'))
        else:
            subprocess.run(['python3',str(ROOT/'scripts/verify-ffmpeg-manifest.py'),str(bundle)],check=True)
        if hashlib.sha256((bundle/'toolchain-manifest.json').read_bytes()).hexdigest()!=manifest.get('toolchainManifestSha256'):raise ValueError('Toolchain manifest changed')
        rclone=directory/'third_party/rclone'
        RCLONE['verify'](rclone,manifest['target'])
        if hashlib.sha256((rclone/'bundle-manifest.json').read_bytes()).hexdigest()!=manifest.get('rcloneManifestSha256'):raise ValueError('rclone manifest changed')
    return manifest

def merge_rclone(bundle,staged):
    """The stage already holds third_party/rclone/bundle.lock.json (a test input), the same contract
    file the bundle carries. Merge only files the bundle carries, each already byte-identical (the
    merge overwrites it); the bundle's exact inventory and hashes are verified with the build."""
    present={p.name for p in staged.iterdir()} if staged.exists() else set()
    carried={p.name for p in bundle.iterdir()}
    if not present<=carried:raise SystemExit('Unexpected files in the staged rclone directory: '+', '.join(sorted(present-carried)))
    changed=sorted(name for name in present if (staged/name).read_bytes()!=(bundle/name).read_bytes())
    if changed:raise SystemExit('Staged rclone files differ from the bundle: '+', '.join(changed))
    shutil.copytree(bundle,staged,dirs_exist_ok=True)

def check_engine(bundle,unsealed):
    """The qualified toolchain, or (only --unsealed) the pinned development engine."""
    if json.loads((bundle/'toolchain-manifest.json').read_text()).get('development'):
        if not unsealed:raise SystemExit('A development engine is allowed only in an --unsealed build')
        DEV_ENGINE['verify'](bundle)
        return True
    subprocess.run(['python3',str(ROOT/'scripts/verify-ffmpeg-manifest.py'),str(bundle)],check=True)
    return False

def release_version(value):
    numeric=r"(?:0|[1-9][0-9]*)"
    identifier=r"(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    pattern=rf"{numeric}\.{numeric}\.{numeric}(?:-{identifier}(?:\.{identifier})*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
    if not re.fullmatch(pattern,value): raise argparse.ArgumentTypeError("Version must be a complete SemVer release version")
    return value

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--version',required=False,type=release_version);parser.add_argument('--os',choices=['linux','darwin','windows'],default='linux');parser.add_argument('--arch',choices=['amd64','arm64'],default='amd64');parser.add_argument('--component',choices=['server'],default='server');parser.add_argument('--ffmpeg-bundle',type=pathlib.Path,default=ROOT/'third_party/ffmpeg/bundle');parser.add_argument('--sonic-bundle',type=pathlib.Path);parser.add_argument('--source-dir',type=pathlib.Path);parser.add_argument('--print-tags',action='store_true',help='print the official Go build tags this builder uses and exit')
    # An internal build: no signing key, no manifest.sig; the only way to build with a development engine.
    parser.add_argument('--unsealed',action='store_true',help='build without the release seal (NOT FOR USE); allows a development engine')
    # A release run tests once: the full -race suite in its gate job, the plain suite on each native-OS build.
    parser.add_argument('--tests',choices=['full','plain','none'],default='full',help='full: -race suite; plain: the suite without -race; none: tested by the release gate')
    args=parser.parse_args()
    # Dry run for the CI guard: the tags below are the ones the build uses, not a
    # second copy of the list.
    if args.print_tags:print(build_tags(args.component));return
    if not args.version:parser.error('--version is required to build')
    key=None if args.unsealed else signing_key()
    root_key,root_pem=official_root()
    if args.component=='server':
        bundle=args.ffmpeg_bundle.resolve()
        development=check_engine(bundle,args.unsealed)
        toolchain=json.loads((bundle/'toolchain-manifest.json').read_text())
        target={'darwin':'macos','linux':'linux','windows':'windows'}[args.os]+'-'+{'amd64':'x64','arm64':'arm64'}[args.arch]
        if toolchain['target']!=target:raise SystemExit('FFmpeg bundle target does not match server')
        if development and args.sonic_bundle:raise SystemExit('Development builds leave Sonic out')
    rclone_bundle=args.ffmpeg_bundle.resolve().parent/('rclone-'+args.os+'-'+args.arch)
    if not rclone_bundle.exists():RCLONE['prepare'](rclone_bundle,args.os+'-'+args.arch,ROOT/'.cache/rclone')
    RCLONE['verify'](rclone_bundle,args.os+'-'+args.arch)
    builder_digest=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    build_id=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')+'-'+uuid.uuid4().hex[:8]
    stage=ROOT/'runtime'/'releases'/build_id;source=stage/'source'
    live=(args.source_dir or ROOT/args.component).resolve()
    if not live.is_relative_to(ROOT) or not (live/'go.mod').is_file():raise SystemExit('Source must be an application module inside this repository.')
    if not re.search(r'^module '+re.escape(module_path(args.component))+r'\s*$',(live/'go.mod').read_text(),re.M):raise SystemExit('Source module does not match release component.')
    snapshot,copied=capture_source(live,stage,root_pem)
    checked_env=controlled_env()
    if args.component=='hosted':
        database_file=pathlib.Path(os.environ.get('PORTICO_TEST_DATABASE_URL_FILE',str(ROOT/'runtime/postgres/test-database-url')))
        if not database_file.is_file():raise SystemExit('Hosted release requires the isolated PostgreSQL integration database.')
        checked_env['PORTICO_TEST_DATABASE_URL_FILE']=str(database_file)
    checked_env['GOWORK']=str(stage/'go.work')
    subprocess.run([str(ROOT/'scripts/env.sh'),'go','mod','verify'],cwd=source,env=checked_env,check=True)
    if args.component=='server' and args.tests!='none':
        helper=stage/'test-helper'
        subprocess.run([str(ROOT/'scripts/env.sh'),'go','build','-mod=readonly','-o',str(helper),'./cmd/server'],cwd=source,env=checked_env,check=True)
        checked_env['PORTICO_PLAYBACK_TEST_HELPER']=str(helper)
    # The 20,000-row metadata boundary test is deliberately exhaustive. Race
    # instrumentation of pure-Go SQLite exceeds Go's default ten-minute limit.
    # --tests none: the release run's gate job ran the full suite on this same commit.
    if args.tests!='none':
        subprocess.run([str(ROOT/'scripts/env.sh'),'go','test',*(['-race'] if args.tests=='full' else []),'-timeout=60m','./...'],cwd=source,env=checked_env,check=True)
    subprocess.run([str(ROOT/'scripts/env.sh'),'sh',str(stage/'scripts/vet-go.sh')],cwd=source,env=checked_env,check=True)
    built_at=datetime.datetime.now(datetime.timezone.utc).isoformat()
    prefix=module_path(args.component)+'/internal/buildinfo.'
    flags=' '.join(f'-X {prefix}{key}={value}' for key,value in {'SourceDigest':snapshot,'BuildID':build_id,'Version':args.version,'BuiltAt':built_at,'Proof':f'PORTICO_RELEASE_V2:{snapshot}:{build_id}:{args.version}:{root_key}:END'}.items())
    flags+=' -X '+('github.com/PorticoMediaServer/portico/server/internal/hostedtrust.OfficialRoot' if args.component=='server' else 'portico.local/hosted/trust.OfficialRoot')+'='+root_key
    env=controlled_env();env.update({'GOWORK':str(stage/'go.work'),'GOOS':args.os,'GOARCH':args.arch,'CGO_ENABLED':'0'})
    # -trimpath: the binary names module paths, never this machine's folders (the snapshot, the module cache, Go's own).
    subprocess.run([str(ROOT/'scripts/env.sh'),'go','build','-tags',build_tags(args.component),'-buildvcs=false','-mod=readonly','-trimpath','-ldflags',flags,'-o',str(stage/('portico-'+args.component+('.exe' if args.os=='windows' else ''))),'./cmd/'+args.component],cwd=source,env=env,check=True)
    if snapshot_digest(source)[0]!=snapshot:raise SystemExit('Source snapshot changed during build.')
    if hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()!=builder_digest:raise SystemExit('Builder changed during execution; discard output and retry.')
    binary=stage/('portico-'+args.component+('.exe' if args.os=='windows' else ''));manifest={'schema':1,'version':args.version,'component':args.component,'buildId':build_id,'sourceDigest':snapshot,'officialRoot':root_key,'builtAt':built_at,'target':args.os+'-'+args.arch,'builderSha256':builder_digest,'sourceFiles':copied,'artifact':{'path':binary.name,'sha256':hashlib.sha256(binary.read_bytes()).hexdigest()},'sealed':not args.unsealed,'tests':args.tests}
    if args.component=='server':
        bundle=args.ffmpeg_bundle.resolve()
        if check_engine(bundle,args.unsealed)!=development:raise SystemExit('FFmpeg bundle changed during build')
        toolchain=json.loads((bundle/'toolchain-manifest.json').read_text())
        if development:manifest['engine']={'development':True,'qualification':toolchain['qualification'],'upstream':toolchain['upstream']}
        target={'darwin':'macos','linux':'linux','windows':'windows'}[args.os]+'-'+{'amd64':'x64','arm64':'arm64'}[args.arch]
        if toolchain['target']!=target:raise SystemExit('FFmpeg bundle target does not match server')
        shutil.copytree(bundle,stage/'third_party/ffmpeg')
        manifest['toolchainManifestSha256']=hashlib.sha256((bundle/'toolchain-manifest.json').read_bytes()).hexdigest()
        if args.sonic_bundle:
            if args.os!='linux':raise SystemExit('Sonic bundles currently support Linux only')
            SONIC['install'](args.sonic_bundle.resolve(),stage/'third_party/ffmpeg',target)
            manifest['sonicManifestSha256']=SONIC['digest'](stage/'third_party/ffmpeg/bin/sonic-manifest.json')
        merge_rclone(rclone_bundle,stage/'third_party/rclone')
        manifest['rcloneManifestSha256']=hashlib.sha256((rclone_bundle/'bundle-manifest.json').read_bytes()).hexdigest()
    (stage/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    if args.unsealed:
        # No manifest.sig: nothing that verifies a release accepts this build.
        check_contents(stage,snapshot,manifest,development=bool(manifest.get('engine',{}).get('development')))
    else:
        (stage/'manifest.sig').write_bytes(key.sign((stage/'manifest.json').read_bytes()));verify_manifest(stage,snapshot,key.public_key().public_bytes_raw())
    print(json.dumps({'directory':str(stage),'sourceDigest':snapshot,'artifactSha256':manifest['artifact']['sha256'],'buildId':build_id}))
if __name__=='__main__':main()
