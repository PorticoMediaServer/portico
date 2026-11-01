#!/usr/bin/env bash
# Sourced by the checked Mac extras. Build the required library and its private
# gettext bootstrap tools from source; no system or Homebrew library substitute.
set -euo pipefail
portico_zvbi() {
 local tools="$CWD/../gettext-tools"
 [ ! -e "$tools" ] && [ ! -L "$tools" ] || { echo 'Fresh private gettext prefix required' >&2; return 1; }
 if declare -F portico_time_start >/dev/null; then portico_time_start portico-gettext-tools; fi
 python3 - "$portico_lock" "$PACKAGES" <<'PYSOURCE'
import hashlib,json,pathlib,shutil,tarfile,sys,urllib.request
entry=json.load(open(sys.argv[1]))['gettextTools'];packages=pathlib.Path(sys.argv[2])
archive=packages/'portico-gettext-source.tar.xz';tree=packages/'portico-gettext'
def check(path):
    if path.is_symlink():raise ValueError('Checked gettext source must not be a link')
    with path.open('rb') as stream:actual=hashlib.file_digest(stream,'sha256').hexdigest()
    if actual!=entry['sha256']:raise ValueError('Checked gettext source changed')
if not archive.exists():
    temporary=archive.with_suffix('.new')
    with urllib.request.urlopen(entry['url'],timeout=60) as source,temporary.open('wb') as out:shutil.copyfileobj(source,out)
    check(temporary);temporary.replace(archive)
check(archive)
if tree.exists() or tree.is_symlink():raise ValueError('Fresh checked gettext source tree required')
unpack=packages/'portico-gettext-unpack';unpack.mkdir()
with tarfile.open(archive) as source:source.extractall(unpack,filter='data')
roots=list(unpack.iterdir())
if len(roots)!=1 or not roots[0].is_dir() or roots[0].is_symlink():raise ValueError('Expected one gettext source root')
roots[0].rename(tree);unpack.rmdir()
PYSOURCE
 cd "$PACKAGES/portico-gettext"
 ./configure --prefix="$tools" --disable-shared --enable-static --disable-java --disable-csharp \
  --without-emacs --disable-openmp --disable-libasprintf --with-xz --without-git --without-cvs
 # Upstream's misc install target supplies autopoint/gettextize and the
 # archived historical macros. Their use needs no libtextstyle or runtime.
 make -C gettext-tools/misc -j "${PORTICO_BUILD_JOBS:-2}"
 make -C gettext-tools/misc install
 export PATH="$tools/bin:$PATH"
 export ACLOCAL_PATH="$tools/share/aclocal:$WORKSPACE/share/aclocal:${ACLOCAL_PATH:-}"
 python3 - "$portico_lock" "$tools" "$PACKAGES/portico-gettext-tools.json" <<'PYTOOLS'
import hashlib,json,pathlib,re,subprocess,sys
entry=json.load(open(sys.argv[1]))['gettextTools'];prefix=pathlib.Path(sys.argv[2])
tool=prefix/'bin/autopoint';version=subprocess.check_output([str(tool),'--version'],text=True)
if not re.search(r'\b'+re.escape(entry['version'])+r'(?:\s|$)',version.splitlines()[0]):raise ValueError('Installed gettext differs from its pin')
record={'version':version,'source':entry,'autopointSHA256':hashlib.sha256(tool.read_bytes()).hexdigest(),
        'buildScope':'gettext-tools/misc scripts and archived macros only; no runtime libraries built','runtimeShipped':False}
pathlib.Path(sys.argv[3]).write_text(json.dumps(record,indent=2)+'\n')
PYTOOLS
 portico_clone zvbi
 ./autogen.sh
 ./configure --prefix="$WORKSPACE" --disable-shared --enable-static --with-pic --without-doxygen \
  --without-x --disable-dvb --disable-bktr --disable-nls --disable-proxy
 make -C src -j "${PORTICO_BUILD_JOBS:-2}"
 make -C src install
 make SUBDIRS=. install
 pkg-config --exact-version=0.2.45 zvbi-0.2
}
portico_zvbi
