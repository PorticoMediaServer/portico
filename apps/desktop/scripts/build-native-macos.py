#!/usr/bin/env python3
import argparse,ctypes,fcntl,hashlib,importlib.util,json,os,pathlib,resource,shutil,signal,subprocess,sys,tarfile,time,wave
parser=argparse.ArgumentParser(description="Owned macOS shared-native development build; no hardware/release qualification")
parser.add_argument('job',type=pathlib.Path)
for name in ['build-tools','rust-tools','cargo-home','dependency-prefix','pkg-config','pkg-config-dir','mpv-archive','prepared-source-archive']:
 parser.add_argument('--'+name,required=True,type=pathlib.Path)
args=parser.parse_args()
def interrupt_job(_signal,_frame):raise KeyboardInterrupt()
for signum in [signal.SIGTERM,signal.SIGHUP]:signal.signal(signum,interrupt_job)
if sys.platform!="darwin":parser.error("requires the actual macOS SDK")
if any(not value.is_absolute() for value in vars(args).values()):parser.error("all build inputs must use absolute paths")
job=args.job.resolve();root=pathlib.Path.home()/"PorticoBuild/desktop-r29"
assert job.parent==root and job.name.startswith("native-shared-f3-")
lock=(root.parent/".xcode-build.lock").open("r+");acquired=False;peak=0
sha=lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
env={"HOME":str(job/"home"),"TMPDIR":str(job/"tmp"),"PATH":os.environ['PATH'],"CI":"1","PYTHONNOUSERSITE":"1","PYTHONDONTWRITEBYTECODE":"1","GIT_CONFIG_NOSYSTEM":"1","GIT_CONFIG_GLOBAL":"/dev/null","GIT_TERMINAL_PROMPT":"0"}
def run(args,cwd=None,timeout=1800,output=False,extra=None):
 global peak
 merged={**env,**(extra or {})}
 process=subprocess.Popen([str(a) for a in args],cwd=cwd or job,env=merged,start_new_session=True,stdout=subprocess.PIPE if output else None)
 start=time.monotonic();last_disk=start
 try:
  while process.poll() is None:
   rows=[tuple(map(int,line.split())) for line in subprocess.check_output(['/bin/ps','-axo','pid=,ppid=,rss='],text=True).splitlines()]
   owned={process.pid}
   for _ in range(20):
    children=owned|{pid for pid,parent,rss in rows if parent in owned}
    if children==owned:break
    owned=children
   current=sum(rss for pid,parent,rss in rows if pid in owned or pid==os.getpid())*1024;peak=max(peak,current)
   if current>1024**3:raise RuntimeError('owned job exceeded approved 1GiB RSS budget')
   if time.monotonic()-last_disk>5:
    used=int(subprocess.check_output(['/usr/bin/du','-sk',str(job)],text=True).split()[0])*1024
    if used>6*1024**3:raise RuntimeError('owned job exceeded approved 6GiB disk budget')
    last_disk=time.monotonic()
   if time.monotonic()-start>timeout:raise TimeoutError('owned build stage timed out')
   time.sleep(.5)
  data=process.stdout.read().decode() if output else ''
  if process.returncode:raise RuntimeError('owned stage failed '+str(process.returncode)+': '+str(args[0]))
  return data.strip()
 except BaseException:
  if process.poll() is None:
   os.killpg(process.pid,signal.SIGINT)
   try:process.wait(timeout=10)
   except subprocess.TimeoutExpired:os.killpg(process.pid,signal.SIGKILL);process.wait()
  raise
 finally:
  if process.stdout:process.stdout.close()
def unpack(archive,destination,select=None,max_bytes=512*1024**2):
 destination.mkdir(parents=True,exist_ok=True)
 with tarfile.open(archive) as tar:
  all_members=tar.getmembers();members=[member for member in all_members if select is None or select(member)]
  if sum(member.size for member in members if member.isfile())>max_bytes:raise RuntimeError('archive exceeds owned extraction bound')
  for member in members:
   name=pathlib.PurePosixPath(member.name)
   if name.is_absolute() or '..' in name.parts:raise RuntimeError('unrooted archive entry')
   if member.islnk() or member.issym():
    target=(destination/pathlib.Path(member.name).parent/member.linkname).resolve()
    if not target.is_relative_to(destination.resolve()):raise RuntimeError('unrooted archive link')
  for member in members:tar.extract(member,destination)
try:
 try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:
  print('REFUSED75 canonical Mac lock busy',lock.read()[:512],flush=True);sys.exit(75)
 acquired=True;lock.seek(0);lock.truncate();lock.write(json.dumps({'owner':'desktop-r29','pid':os.getpid(),'phase':'authorized-shared102-soxr-tls','startedAt':time.time()}));lock.flush()
 for name in ['home','tmp','tools']:(job/name).mkdir()
 disk=shutil.disk_usage(root)
 if disk.free<26*1024**3 or (disk.used+6*1024**3)/disk.total>.8:sys.exit(75)
 print(run(['/bin/df','-h','/System/Volumes/Data'],output=True),flush=True)
 print(run(['/usr/bin/memory_pressure'],output=True).splitlines()[-1],flush=True)
 app=job/'app';contract=job/'contract';unpack(job/'app.tar',app);unpack(job/'contract.tar',contract)
 native=app/'apps/desktop';deps=args.dependency_prefix
 python=str(args.build_tools/'python3.14');meson=str(args.build_tools/'meson');ninja=str(args.build_tools/'ninja');pkg=str(args.pkg_config)
 prefix=job/'prefix';prefix.mkdir();(job/'build').mkdir()
 locked=json.loads((native/'native-deps.lock.json').read_text())['ffmpeg']
 assert sha(contract/'third_party/ffmpeg/sources.lock.json')==locked['sourceLockSha256']
 assert sha(contract/'scripts/prepare-ffmpeg-source.py')==locked['preparerSha256']
 assert sha(args.prepared_source_archive)==locked['preparedArchiveSha256']
 unpack(args.prepared_source_archive,job/'ffmpeg-input');prepared=job/'ffmpeg-input/ffmpeg'
 receipt=prepared/'portico-source-receipt.json';assert sha(receipt)==locked['preparedReceiptSha256']
 # Verify every canonical source byte/symlink before configuring. Never normalize
 # a platform-generated patch tree or waive the producer's immutable receipt.
 verify="""import importlib.util,json,pathlib,sys
contract=pathlib.Path(sys.argv[1]);source=pathlib.Path(sys.argv[2])
spec=importlib.util.spec_from_file_location('canonical_source',contract/'scripts/prepare-ffmpeg-source.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
receipt=json.loads((source/'portico-source-receipt.json').read_text());lock=json.loads((contract/'third_party/ffmpeg/sources.lock.json').read_text())
assert receipt['sourceCommit']==lock['ffmpeg']['commit']
assert receipt['patches']==lock['ffmpeg']['patches'] and receipt['postSeriesPatches']==lock['ffmpeg']['postSeriesPatches']
assert receipt['files']==module.tree_files(source)
print('EXACT_102_CANONICAL_SOURCE_INVENTORY_PASS')
"""
 run([python,'-c',verify,contract,prepared])
 cmake_archive=job/'tools/cmake-3.31.12-macos-universal.tar.gz'
 run(['/usr/bin/curl','--silent','--show-error','--fail','--location','--proto','=https','--proto-redir','=https','--max-time','240','--max-filesize','79954716','https://github.com/Kitware/CMake/releases/download/v3.31.12/cmake-3.31.12-macos-universal.tar.gz','-o',cmake_archive])
 assert cmake_archive.stat().st_size==79954716 and sha(cmake_archive)=='799af7fd545db9bf1b9cfe72f8095880e727a2d4e0df0e3dffc3bc7b95c2d3b0'
 # Only CLI executables/modules are needed. Preserve their exact archive bytes;
 # leave Qt/GUI payload unextracted to keep archive+tool below the approved cap.
 def tool_member(member):
  parts=pathlib.PurePosixPath(member.name).parts
  if 'Contents' not in parts:return False
  tail=parts[parts.index('Contents')+1:]
  return bool(tail) and (tail[0]=='share' or tail[0]=='bin' and (len(tail)==1 or tail[1] in ['cmake','ctest','cpack']))
 unpack(cmake_archive,job/'tools',select=tool_member,max_bytes=400*1024**2-cmake_archive.stat().st_size)
 cmake=next((job/'tools').glob('*/CMake.app/Contents/bin/cmake'));ctest=cmake.with_name('ctest')
 run(['/usr/bin/codesign','--verify','--strict',cmake]);assert '3.31.12' in run([cmake,'--version'],output=True)
 assert int(subprocess.check_output(['/usr/bin/du','-sk',str(job/'tools')],text=True).split()[0])*1024<=400*1024**2
 compiler=run(['/usr/bin/xcrun','--find','clang'],output=True);cxx=run(['/usr/bin/xcrun','--find','clang++'],output=True);sdk=run(['/usr/bin/xcrun','--sdk','macosx','--show-sdk-path'],output=True)
 env.update(CC=compiler,CXX=cxx,SDKROOT=sdk,PKG_CONFIG=pkg,NINJA=ninja,MACOSX_DEPLOYMENT_TARGET='13.0',PKG_CONFIG_LIBDIR=str(prefix/'lib/pkgconfig')+':'+str(args.pkg_config_dir),PKG_CONFIG_PATH='')
 soxr=job/'soxr';run(['/usr/bin/git','init','--quiet',soxr]);run(['/usr/bin/git','-C',soxr,'remote','add','origin','https://git.code.sf.net/p/soxr/code'])
 run(['/usr/bin/git','-C',soxr,'fetch','--quiet','--depth=1','origin','945b592b70470e29f917f4de89b4281fbbd540c0']);run(['/usr/bin/git','-C',soxr,'checkout','--quiet','--detach','FETCH_HEAD'])
 assert run(['/usr/bin/git','-C',soxr,'rev-parse','HEAD'],output=True)=='945b592b70470e29f917f4de89b4281fbbd540c0'
 soxr_build=job/'build/soxr'
 run([cmake,'-S',soxr,'-B',soxr_build,'-G','Ninja','-DCMAKE_MAKE_PROGRAM='+ninja,'-DCMAKE_C_COMPILER='+compiler,'-DCMAKE_BUILD_TYPE=Release','-DCMAKE_OSX_ARCHITECTURES=arm64','-DCMAKE_OSX_SYSROOT='+sdk,'-DCMAKE_INSTALL_PREFIX='+str(prefix),'-DBUILD_SHARED_LIBS=ON','-DWITH_OPENMP=OFF','-DWITH_LSR_BINDINGS=OFF','-DWITH_AVFFT=OFF','-DBUILD_TESTS=ON','-DBUILD_EXAMPLES=OFF','-DWITH_DEV_TRACE=OFF'])
 run([cmake,'--build',soxr_build,'--parallel','1']);run([ctest,'--test-dir',soxr_build,'-R','^1-delay-clear$','--no-tests=error','--output-on-failure']);run([cmake,'--install',soxr_build])
 print('SOXR_SHARED_ARM64_BUILD_PASS',flush=True)
 ffbuild=job/'build/ffmpeg';ffbuild.mkdir()
 run([prepared/'configure','--prefix='+str(prefix),'--cc='+compiler,'--cxx='+cxx,'--pkg-config='+pkg,'--arch=arm64','--target-os=darwin','--enable-shared','--disable-static','--disable-programs','--disable-doc','--disable-debug','--enable-gpl','--enable-version3','--disable-autodetect','--enable-network','--enable-securetransport','--enable-libsoxr','--enable-libass','--enable-videotoolbox','--enable-audiotoolbox','--enable-zlib','--disable-protocols','--enable-protocol=file,pipe,http,https,tcp,tls,data','--optflags=-O2','--extra-cflags=-I'+str(prefix/'include'),'--extra-ldflags=-L'+str(prefix/'lib')],cwd=ffbuild)
 run(['/usr/bin/make','-s','-j1'],cwd=ffbuild);run(['/usr/bin/make','-s','install'],cwd=ffbuild)
 print('FFMPEG_SHARED_102_BUILD_PASS',flush=True)
 mpv_archive=args.mpv_archive;assert sha(mpv_archive)=='ee21092a5ee427353392360929dc64645c54479aefdb5babc5cfbb5fad626209'
 mpv_root=job/'mpv-source';unpack(mpv_archive,mpv_root);mpv_source=next(mpv_root.iterdir());mpv_build=job/'build/mpv'
 # The app supplies fixed native commands, never a scripting interface. Disable
 # unused interpreters rather than accidentally admitting host Lua/JS packages.
 run([meson,'setup',mpv_build,mpv_source,'--prefix='+str(prefix),'--buildtype=release','-Doptimization=2','-Db_lto=false','-Dlibmpv=true','-Dcplayer=false','-Dtests=false','-Dlua=disabled','-Djavascript=disabled','-Dcocoa=enabled','-Dvulkan=enabled','-Dhtml-build=disabled','-Dmanpage-build=disabled'])
 run([ninja,'-C',mpv_build,'-j1']);run([meson,'install','-C',mpv_build,'--no-rebuild'])
 stage=job/'native-stage';run(['/usr/bin/python3',native/'scripts/stage-native-macos.py','--mpv',prefix/'lib/libmpv.2.dylib','--ffmpeg-prefix',prefix,'--dependency-directory',deps/'lib','--ffmpeg-receipt',receipt,'--lock',native/'native-deps.lock.json','--output',stage])
 for name,major in {'avcodec':62,'avformat':62,'avutil':60,'avfilter':11,'avdevice':62,'swscale':9,'swresample':6}.items():
  library=ctypes.CDLL(str(next(stage.glob('lib'+name+'.'+str(major)+'.dylib'))));version=getattr(library,name+'_version');version.restype=ctypes.c_uint;assert version()>>16==major
 avformat=ctypes.CDLL(str(stage/'libavformat.62.dylib'));avformat.avformat_configuration.restype=ctypes.c_char_p;config=avformat.avformat_configuration().decode()
 assert '--enable-securetransport' in config and '--enable-libsoxr' in config and '--enable-libass' in config
 avformat.avio_enum_protocols.restype=ctypes.c_char_p;avformat.avio_enum_protocols.argtypes=[ctypes.POINTER(ctypes.c_void_p),ctypes.c_int];opaque=ctypes.c_void_p();protocols=[]
 while True:
  item=avformat.avio_enum_protocols(ctypes.byref(opaque),0)
  if not item:break
  protocols.append(item.decode())
 assert 'https' in protocols and 'tls' in protocols
 assert 'libsoxr.' in run(['/usr/bin/otool','-L',stage/'libswresample.6.dylib'],output=True)
 print('SHARED_ABI_AND_LINKED_TLS_SOXR_ASS_PASS; live TLS/output/HDR not measured',flush=True)
 pcm=job/'owned-pcm.wav'
 with wave.open(str(pcm),'wb') as wav:wav.setnchannels(2);wav.setsampwidth(2);wav.setframerate(48000);wav.writeframes(b'\x00'*48000*4*3)
 testenv={'CARGO_HOME':str(args.cargo_home),'RUSTUP_TOOLCHAIN':'1.99.0','CARGO_TARGET_DIR':str(job/'build/engine'),'CARGO_BUILD_JOBS':'1','PORTICO_TEST_MPV_LIBRARY':str(stage/'libmpv.2.dylib'),'PORTICO_TEST_PCM_FIXTURE':str(pcm)}
 cargo=str(args.rust_tools/'cargo');rustc=str(args.rust_tools/'rustc');testenv['RUSTC']=rustc
 for name in ['constrained_local_source_plays_pcm_and_never_follows_network_playlist','native_http_progressive_plays_but_manifest_contents_never_open_another_resource']:
  run([cargo,'test','--quiet','--locked','--manifest-path',native/'src-tauri/engine/Cargo.toml','--lib',name,'--','--ignored','--test-threads=1'],extra=testenv)
 artifact=root/('artifacts/'+job.name);artifact.mkdir(parents=True,exist_ok=False);shutil.copytree(stage,artifact/'native-stage')
 print('NATIVE_SHARED_F3_102_DEV_BUILD_AND_ALLOWLIST_PASS peakRSSBytes='+str(peak),flush=True)
finally:
 if acquired:lock.seek(0);lock.truncate();lock.flush();fcntl.flock(lock,fcntl.LOCK_UN)
 lock.close();shutil.rmtree(job);print('OWNED_CMAKE_TOOL_AND_JOB_SCRATCH_REMOVED',flush=True)
