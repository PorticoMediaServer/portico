#!/usr/bin/env python3
"""Compile the production Android transport against cached jars, then real HTTP IO.

Run through the existing Android quick wrapper. Android/RN value conversion and
TLS delegation are test boundaries; this does not establish device/TLS acceptance.
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
runtime = [artifact('org.jetbrains.kotlin', 'kotlin-stdlib', version, '.jar'),
           artifact('com.squareup.okhttp3', 'okhttp', '4.12.0', '.jar'),
           artifact('com.squareup.okio', 'okio-jvm', '3.6.0', '.jar')]
annotations = artifact('org.jetbrains', 'annotations', '13.0', '.jar')
compiler.add(annotations)
args.scratch.mkdir(parents=True, exist_ok=True)
work = Path(tempfile.mkdtemp(prefix='transport-', dir=args.scratch))
try:
    jar = work / 'transport-test.jar'
    source = native / 'android/app/src/main/java/tv/getportico/portico/native/PorticoTransport.kt'
    surface = (source.parent / 'PorticoRuntime.kt').read_text()
    start = surface.index('  @ReactMethod\n  fun networkStreamOpen(')
    end = surface.index('  @ReactMethod\n  fun openNetworkStream(', start)
    methods = surface[start:end]
    if methods.count('@ReactMethod') != 2:
        raise RuntimeError('Unexpected native Reading method boundary')
    # Compile the actual two methods, not a hand-maintained policy copy. The
    # temporary class supplies only the RN base-class/value boundary for JVM IO.
    runtime_surface = work / 'RuntimeSurface.kt'
    runtime_surface.write_text('package tv.getportico.portico.native\nimport com.facebook.react.bridge.*\ninternal class RuntimeSurface {\n' + methods + '\n}\n')
    fixtures = sorted((native / 'test/reading-stream-android').glob('*.kt'))
    subprocess.run(['java', '-Xmx768m', '-cp', os.pathsep.join(map(str, sorted(compiler))),
                    'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler', '-no-stdlib', '-no-reflect', '-jvm-target', '17',
                    '-classpath', os.pathsep.join(map(str, runtime + [annotations])), '-d', str(jar), str(source), str(runtime_surface), *map(str, fixtures)], check=True)
    subprocess.run(['java', '-Xmx256m', '-cp', os.pathsep.join(map(str, [jar] + runtime)),
                    'tv.getportico.portico.native.TransportTestKt'], check=True, timeout=75)
finally:
    shutil.rmtree(work)
