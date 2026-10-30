#!/usr/bin/env python3
"""Cached Android SDK/RN source compile + production pure admission/math vectors.

Run with the existing Android quick admission wrapper. No Gradle/APK/device job,
target attachment, app launch, native paint emulation or private fixture input.
"""
import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as ET
from zipfile import ZipFile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--scratch', type=Path, required=True)
args = parser.parse_args()
native = Path(__file__).resolve().parents[1]
gradle_home = Path(os.environ.get('GRADLE_USER_HOME', Path.home() / '.gradle'))
cache = gradle_home / 'caches/modules-2/files-2.1'
gradle = (native / 'android/build.gradle').read_text()
version = re.search(r'kotlinVersion\s*=\s*"([^"]+)"', gradle).group(1)
sdk_version = re.search(r'compileSdkVersion\s*=\s*(\d+)', gradle).group(1)
sdk = Path(os.environ.get('ANDROID_HOME', os.environ.get('ANDROID_SDK_ROOT', Path.home() / 'Android/Sdk')))
android_jar = sdk / 'platforms' / ('android-' + sdk_version) / 'android.jar'

def artifact(group, name, version, suffix):
    files = list((cache / group / name / version).glob('*/' + name + '-' + version + suffix))
    if len(files) != 1:
        raise RuntimeError('Required declared cache artifact absent: ' + name + ':' + version)
    return files[0]

compiler = {artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.jar')}
pom = ET.parse(artifact('org.jetbrains.kotlin', 'kotlin-compiler-embeddable', version, '.pom'))
ns = {'m': 'http://maven.apache.org/POM/4.0.0'}
for dep in pom.findall('m:dependencies/m:dependency', ns):
    compiler.add(artifact(*[dep.find('m:' + key, ns).text for key in ('groupId', 'artifactId', 'version')], '.jar'))
annotations = artifact('org.jetbrains', 'annotations', '13.0', '.jar')
compiler.add(annotations)
stdlib = artifact('org.jetbrains.kotlin', 'kotlin-stdlib', version, '.jar')
rn_version = __import__('json').loads((native / 'node_modules/react-native/package.json').read_text())['version']
rn_jars = sorted((gradle_home / 'caches').glob('*/transforms/*/transformed/react-android-' + rn_version + '-debug-api.jar'))
if not android_jar.is_file() or not rn_jars:
    raise RuntimeError('Actual declared SDK/RN API cache is required; no synthetic Android/RN API')
source = native / 'android/app/src/main/java/tv/getportico/portico/native/PorticoLayoutObservation.kt'
if re.search(r'\.systemFontFamilyName\b|\.getSystemFontFamilyName\(', source.read_text()):
    raise RuntimeError('System family label is not associated typographic name-table evidence')
package = source.with_name('PorticoPackage.kt').read_text()
if '(if (BuildConfig.DEBUG) listOf(PorticoLayoutObservation(reactContext)) else emptyList())' not in package:
    raise RuntimeError('Observation module must be registered only in DEBUG')
args.scratch.mkdir(parents=True, exist_ok=True)
work = Path(tempfile.mkdtemp(prefix='observation-', dir=args.scratch))
try:
    config = work / 'BuildConfig.kt'
    config.write_text('package tv.getportico.portico\nobject BuildConfig {const val DEBUG=true}\n')
    policy = work / 'Policy.kt'
    policy.write_text('package tv.getportico.portico.native\nimport java.util.ArrayDeque\nimport java.util.Locale\ninternal object LayoutObservationPolicy {' + source.read_text().split('internal object LayoutObservationPolicy {', 1)[1])
    def compile(output, files, classpath):
        subprocess.run(['java', '-Xmx768m', '-cp', os.pathsep.join(map(str, sorted(compiler))),
                        'org.jetbrains.kotlin.cli.jvm.K2JVMCompiler', '-no-stdlib', '-no-reflect', '-jvm-target', '17',
                        '-classpath', os.pathsep.join(map(str, classpath)), '-d', str(output), *map(str, files)], check=True, timeout=120)
    fresco_version = re.search(r'^fresco\s*=\s*"([^"]+)"', (native / 'node_modules/react-native/gradle/libs.versions.toml').read_text(), re.M).group(1)
    fresco_jars = []
    for name in ['drawee', 'ui-core', 'fbcore']:
        jar = work / (name + '.jar')
        with ZipFile(artifact('com.facebook.fresco', name, fresco_version, '.aar')) as aar:
            jar.write_bytes(aar.read('classes.jar'))
        fresco_jars.append(jar)
    compile(work / 'bridge.jar', [source, config], [stdlib, annotations, android_jar, rn_jars[0], *fresco_jars])
    print('Production observation bridge: actual Android SDK' + sdk_version + '/RN' + rn_version + ' compile PASS', flush=True)
    tests = native / 'test/layout-observation-android/ObservationPolicyTest.kt'
    compile(work / 'policy.jar', [policy, tests], [stdlib, annotations])
    subprocess.run(['java', '-Xmx256m', '-cp', os.pathsep.join(map(str, [work / 'policy.jar', stdlib])),
                    'tv.getportico.portico.native.ObservationPolicyTestKt'], check=True, timeout=30)
finally:
    shutil.rmtree(work)
