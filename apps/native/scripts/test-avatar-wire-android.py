#!/usr/bin/env python3
"""Compile the production avatar decoder and check canonical image bytes/bounds.

Run under portico-heavy. Java's base64 decoder supplies the Android value boundary
for canonical inputs; this does not establish device or malformed-codec parity.
"""
import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--scratch', type=Path, required=True)
args = parser.parse_args()
native = Path(__file__).resolve().parents[1]
cache = Path(os.environ.get('GRADLE_USER_HOME', Path.home() / '.gradle')) / 'caches/modules-2/files-2.1'
version = re.search(r'kotlinVersion\s*=\s*"([^"]+)"', (native / 'android/build.gradle').read_text()).group(1)


def artifact(group, name, version, suffix):
    files = list((cache / group / name / version).glob('*/' + name + '-' + version + suffix))
    if len(files) != 1:
        raise RuntimeError('Required declared Android cache artifact absent: ' + name + ':' + version)
    return files[0]


compiler = {artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.jar')}
pom = ET.parse(artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.pom'))
ns = {'m': 'http://maven.apache.org/POM/4.0.0'}
for dep in pom.findall('m:dependencies/m:dependency', ns):
    values = [dep.find('m:' + key, ns).text for key in ('groupId', 'artifactId', 'version')]
    compiler.add(artifact(*values, '.jar'))
stdlib = artifact('org.jetbrains.kotlin', 'kotlin-stdlib', version, '.jar')
annotations = artifact('org.jetbrains', 'annotations', '13.0', '.jar')
compiler.add(annotations)
args.scratch.mkdir(parents=True, exist_ok=True)
work = Path(tempfile.mkdtemp(prefix='avatar-wire-', dir=args.scratch))
try:
    source = (native / 'android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt').read_text()
    start = source.index('  private fun avatarUploadBody(')
    end = source.index('  private fun boundedRequest(', start)
    surface = work / 'RuntimeSurface.kt'
    surface.write_text('package tv.getportico.portico.native\nimport android.util.Base64\n'
                       'internal object AvatarWireRuntime {\n' + source[start:end] +
                       '\nfun decode(body: String?): ByteArray? = avatarUploadBody(body)\n}\n')
    jar = work / 'avatar-wire-test.jar'
    fixtures = sorted((native / 'test/avatar-wire-android').glob('*.kt'))
    subprocess.run(['java', '-Xmx768m', '-cp', os.pathsep.join(map(str, sorted(compiler))),
                    'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler', '-no-stdlib', '-no-reflect', '-jvm-target', '17',
                    '-classpath', os.pathsep.join(map(str, [stdlib, annotations])), '-d', str(jar),
                    str(surface), *map(str, fixtures)], check=True)
    subprocess.run(['java', '-Xmx256m', '-cp', os.pathsep.join(map(str, [jar, stdlib])),
                    'tv.getportico.portico.native.AvatarWireTestKt'], check=True, timeout=45)
finally:
    shutil.rmtree(work)
