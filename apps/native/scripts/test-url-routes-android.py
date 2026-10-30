#!/usr/bin/env python3
"""Check the actual Android DEBUG route decision using cached compiler/SDK jars.

No Android URI parser is emulated. The production filter compiles against the
installed SDK; pure forwarding vectors run in DEBUG and release configurations.
Run through the existing Android quick admission wrapper; no APK/device work.
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
gradle = (native / 'android/build.gradle').read_text()
version = re.search(r'kotlinVersion\s*=\s*"([^"]+)"', gradle).group(1)
sdk_version = re.search(r'compileSdkVersion\s*=\s*(\d+)', gradle).group(1)
sdk = Path(os.environ.get('ANDROID_HOME', os.environ.get('ANDROID_SDK_ROOT', Path.home() / 'Android/Sdk')))
android_jar = sdk / 'platforms' / ('android-' + sdk_version) / 'android.jar'
if not android_jar.is_file():
    raise RuntimeError('Required declared Android SDK cache absent')

def artifact(group, name, pinned_version, suffix):
    files = list((cache / group / name / pinned_version).glob('*/' + name + '-' + pinned_version + suffix))
    if len(files) != 1:
        raise RuntimeError('Required declared Android cache artifact absent: ' + name + ':' + pinned_version)
    return files[0]

compiler = {artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.jar')}
pom = ET.parse(artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.pom'))
ns = {'m': 'http://maven.apache.org/POM/4.0.0'}
for dep in pom.findall('m:dependencies/m:dependency', ns):
    values = [dep.find('m:' + key, ns).text for key in ('groupId', 'artifactId', 'version')]
    compiler.add(artifact(*values, '.jar'))
annotations = artifact('org.jetbrains', 'annotations', '13.0', '.jar')
compiler.add(annotations)
runtime = [artifact('org.jetbrains.kotlin', 'kotlin-stdlib', version, '.jar'), android_jar, annotations]
source = native / 'android/app/src/main/java/tv/getportico/portico/PorticoUrlRoutes.kt'
activity = (source.parent / 'MainActivity.kt').read_text()
cold = activity[activity.index('override fun onCreate('):activity.index('override fun onPictureInPictureModeChanged(')]
warm = activity[activity.index('override fun onNewIntent('):]
assert cold.index('intent = PorticoUrlRoutes.filtered(intent)') < cold.index('super.onCreate(null)')
assert warm.index('PorticoUrlRoutes.filtered(intent)') < warm.index('setIntent(allowed)') < warm.index('super.onNewIntent(allowed)')
print('Initial/new Intent delivery uses the same production filter before RN delivery: PASS')
args.scratch.mkdir(parents=True, exist_ok=True)
work = Path(tempfile.mkdtemp(prefix='url-routes-', dir=args.scratch))
try:
    for debug in (True, False):
        variant = work / ('debug' if debug else 'release')
        variant.mkdir()
        config = variant / 'BuildConfig.kt'
        config.write_text('package tv.getportico.portico\ninternal object BuildConfig { const val DEBUG = ' + str(debug).lower() + ' }\n')
        jar = variant / 'route-test.jar'
        subprocess.run(['java', '-Xmx768m', '-cp', os.pathsep.join(map(str, sorted(compiler))),
                        'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler', '-no-stdlib', '-no-reflect', '-jvm-target', '17',
                        '-classpath', os.pathsep.join(map(str, runtime)), '-d', str(jar), str(source), str(config),
                        str(native / 'test/url-routes-android/MeasurementRouteTest.kt')], check=True, timeout=120)
        subprocess.run(['java', '-Xmx256m', '-cp', os.pathsep.join(map(str, [jar] + runtime)),
                        'tv.getportico.portico.MeasurementRouteTestKt'], check=True, timeout=20)
finally:
    shutil.rmtree(work)
