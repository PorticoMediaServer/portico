#!/usr/bin/env python3
"""Build owned Apple suites against real Pods and run on disposable iOS/tvOS simulators.

Use --ios-products DIR for an existing Debug-iphonesimulator build products directory,
--pods-root DIR for its installed Pods, and --fixtures DIR (audio-decode/*.json,
audio-render/render-test.wav, video/snapshot-red-blue.mp4). No app or Pods build is started.
Receipts require a clean checkout, exit zero and each recipe's complete pass criteria.
"""
import argparse
import base64
from contextlib import contextmanager
import hashlib
import http.server
import json
import os
from pathlib import Path
import plistlib
import re
import shlex
import shutil
import ssl
import subprocess
import sys
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'scripts/platform-tests.json'
RECIPES = ROOT / 'scripts/owned-apple-recipes.json'
TARGETS = {'iphonesimulator': 'arm64-apple-ios15.1-simulator',
           'appletvsimulator': 'arm64-apple-tvos15.1-simulator'}


def owned_sources(manifest=MANIFEST):
    return [entry['source'] for entry in json.loads(manifest.read_text())['apple'] if entry['execution'] == 'owned-apple']


def json_documents(stdout):
    """Decode complete objects (including pretty printed JSON), ignoring diagnostic lines."""
    decoder, documents, offset = json.JSONDecoder(), [], 0
    while offset < len(stdout):
        start = stdout.find('{', offset)
        if start < 0:
            break
        try:
            value, end = decoder.raw_decode(stdout, start)
        except ValueError:
            offset = start + 1
            continue
        documents.append(value)
        offset = end
    return documents


def outcome(stdout, exit_code, verdict=None):
    """Require the recipe's complete case set; diagnostic booleans are not assertions."""
    if exit_code != 0:
        return False, 0
    verdict = verdict or {'kind': 'summary', 'minimum': 1}
    kind = verdict['kind']
    if kind == 'reader':
        rows = re.findall(r'^Apple reader assets: (\d+) checks, (\d+) failures$', stdout, re.M)
        return (len(rows) == 1 and rows[0] == ('36', '0'), int(rows[0][0]) if rows else 0)
    documents = json_documents(stdout)
    if kind == 'fields':
        rows = [row for row in documents if all(key in row for key in verdict['true'])]
        count = len(rows)
        ok = count == verdict.get('cases', 1)
        for row in rows:
            ok = ok and all(row.get(key) is True for key in verdict['true'])
            ok = ok and all(row.get(key) is False for key in verdict.get('false', []))
        expected = verdict.get('expectedCases')
        if expected:
            # Exact identities prevent one passing configuration repeated thirteen times
            # from standing in for the full output-rate/channel/EQ case matrix.
            keys = sorted(expected[0])
            actual = [json.dumps({key: row.get(key) for key in keys}, sort_keys=True) for row in rows]
            wanted = [json.dumps(row, sort_keys=True) for row in expected]
            ok = ok and sorted(actual) == sorted(wanted)
        return ok, count * (len(verdict['true']) + len(verdict.get('false', [])))
    if kind == 'decode':
        rows = [row for row in documents if isinstance(row.get('fixtures'), list)]
        if len(rows) != 1:
            return False, 0
        fixtures = rows[0]['fixtures']
        if not all(isinstance(row, dict) and isinstance(row.get('file'), str) for row in fixtures):
            return False, 0
        names = sorted(row.get('file', '') for row in fixtures)
        expected = verdict.get('files', [])
        ok = len(fixtures) >= 7 and names == sorted(expected) and all(row.get('status') == 'pass' for row in fixtures)
        albums = rows[0].get('albums', {})
        if not isinstance(albums, dict) or not all(isinstance(row, dict) for row in albums.values()):
            return False, 0
        ok = ok and all(row.get('allTracksPass') is True and row.get('worstBoundaryFrames') == 0
                        and row.get('tracks', 0) >= 2 for row in albums.values())
        return ok, len(fixtures) + len(albums)
    rows = [row for row in documents if 'assertions' in row and 'failures' in row]
    if len(rows) != 1:
        return False, 0
    count, failures = rows[0]['assertions'], rows[0]['failures']
    valid = type(count) is int and type(failures) is int
    return valid and count >= verdict.get('minimum', 1) and failures == 0, count if type(count) is int else 0


def substitute(value, places):
    for key, path in places.items():
        value = value.replace('{' + key + '}', str(path))
    return value


def plan(source, recipe, work, fixtures, products=None, pods=None):
    """No build invocation here; dependencies are prepared by the caller's app build."""
    if not recipe or not recipe.get('runnable'):
        return None, (recipe or {}).get('reason', 'no recipe')
    if recipe.get('fixtures') and fixtures is None:
        return None, 'needs --fixtures: ' + recipe['fixtures']
    if recipe.get('pods') and (products is None or pods is None):
        return None, 'needs --ios-products and --pods-root from a real Pods build'
    if recipe.get('media') and products is None:
        return None, 'needs --ios-products containing the real subtitle/codec frameworks'
    places = {'tmp': work, 'fixtures': fixtures or '', 'root': ROOT}
    sdk = recipe.get('sdk', 'iphonesimulator')
    app = work / (Path(source).stem + '.app') if recipe.get('scene') else None
    binary = (app or work) / Path(source).stem
    if app:
        app.mkdir(parents=True, exist_ok=True)
    swift = source.endswith('.swift')
    build = ['xcrun', '--sdk', sdk, 'swiftc' if swift else 'clang']
    build += ['-Onone'] if swift else ['-fobjc-arc', '-fblocks', '-fmodules', '-DDEBUG=1']
    if sdk in TARGETS:
        build += ['-target', TARGETS[sdk]]
    if recipe.get('pods'):
        public = pods / 'Headers/Public'
        build += ['-I' + str(public)] + ['-I' + str(path) for path in sorted(public.iterdir()) if path.is_dir()]
        # Pull ObjC categories from React-Core as well as classes. Other archives supply
        # genuine RN dependencies on demand, without loading unrelated third-party modules.
        core = products / 'React-Core/libReact-Core.a'
        build += ['-Wl,-force_load,' + str(core)]
        build += [str(path) for path in sorted(products.glob('*/*.a')) if path != core]
        build += ['-lc++', '-licucore', '-framework', 'JavaScriptCore', '-framework', 'SystemConfiguration',
                  '-framework', 'CFNetwork', '-framework', 'CoreGraphics', '-framework', 'QuartzCore']
        hermes = products / 'XCFrameworkIntermediates/hermes-engine/Pre-built'
        if (hermes / 'hermesvm.framework').exists():
            build += ['-F' + str(hermes), '-framework', 'hermesvm',
                      '-Wl,-rpath,' + str(hermes), '-Wl,-rpath,@executable_path/Frameworks']
    if recipe.get('media'):
        build += ['-F' + str(products), '-Wl,-rpath,' + str(products), '-Wl,-rpath,@executable_path/Frameworks', '-lsqlite3']
        for framework in ['Libporticosubtitle', 'Libass', 'Libavformat', 'Libavcodec', 'Libavutil']:
            build += ['-framework', framework]
    build += recipe.get('flags', [])
    for framework in recipe.get('frameworks', []):
        build += ['-framework', framework]
    build += [str(ROOT / item) for item in recipe.get('sources', [])] + [str(ROOT / source), '-o', str(binary)]
    runs = []
    for run in recipe.get('runs', [{}]):
        arguments = [substitute(argument, places) for argument in run.get('args', [])]
        environment = {key: substitute(value, places) for key, value in run.get('env', {}).items()}
        runs.append((arguments, environment, run.get('verdict', recipe['verdict'])))
    return {'build': build, 'binary': binary, 'sdk': sdk, 'runs': runs, 'app': app,
            'products': products, 'places': places}, None


def checked(argv, **kwargs):
    return subprocess.run(argv, check=True, capture_output=True, text=True, timeout=120, **kwargs).stdout.strip()


def package_app(command, recipe):
    app, binary = command['app'], command['binary']
    identifier = 'tv.getportico.tests.' + binary.name.replace('apple-', '').replace('-tests', '')
    info = {'CFBundleIdentifier': identifier, 'CFBundleExecutable': binary.name, 'CFBundleName': binary.name,
            'CFBundlePackageType': 'APPL', 'CFBundleVersion': '1', 'CFBundleShortVersionString': '1',
            'LSRequiresIPhoneOS': True, 'UIDeviceFamily': [1, 2], 'UILaunchScreen': {},
            'UIApplicationSceneManifest': {'UIApplicationSupportsMultipleScenes': False, 'UISceneConfigurations': {
                'UIWindowSceneSessionRoleApplication': [{'UISceneConfigurationName': 'Test',
                                                       'UISceneDelegateClassName': recipe['scene']}]}},
            'UISupportedInterfaceOrientations': ['UIInterfaceOrientationPortrait']}
    (app / 'Info.plist').write_bytes(plistlib.dumps(info))
    for source, name in recipe.get('resources', {}).items():
        shutil.copy2(substitute(source, command['places']), app / name)
    # Copy only dynamic frameworks; static media frameworks stay in the linked executable.
    for framework in (command['products'] or app).rglob('*.framework'):
        executable = framework / framework.stem
        if executable.is_file() and 'dynamically linked' in checked(['file', str(executable)]):
            destination = app / 'Frameworks' / framework.name
            if destination.exists():
                continue
            destination.parent.mkdir(exist_ok=True)
            shutil.copytree(framework, destination, symlinks=True)
            checked(['codesign', '-s', '-', '--force', str(destination)])
    checked(['codesign', '-s', '-', '--force', str(app)])
    return identifier


def execute(command, arguments, environment, device, timeout, recipe):
    env = dict(os.environ, **{'SIMCTL_CHILD_' + key: value for key, value in environment.items()})
    simctl = ['xcrun', 'simctl']
    if command['app']:
        identifier = package_app(command, recipe)
        try:
            checked(simctl + ['install', device, str(command['app'])])
            container = Path(checked(simctl + ['get_app_container', device, identifier, 'data']))
            result_file = container / 'Documents/result.json'
            result_file.unlink(missing_ok=True)
            result = subprocess.run(simctl + ['launch', '--console', '--terminate-running-process', device, identifier, *arguments],
                                    env=env, capture_output=True, text=True, timeout=timeout)
            # simctl's exit status alone does not establish the application exit status.
            if not result_file.is_file():
                result.returncode = 1
                result.stderr += '\nApplication exited without Documents/result.json\n'
                return result
            actual = json.loads(result_file.read_text())
            if type(actual.get('exit')) is not int or actual['exit'] != 0:
                result.returncode = 1
            result.stdout += '\n' + json.dumps(actual) + '\n'
            return result
        finally:
            subprocess.run(simctl + ['terminate', device, identifier], capture_output=True, timeout=30)
            subprocess.run(simctl + ['uninstall', device, identifier], capture_output=True, timeout=30)
    argv = simctl + ['spawn', device, str(command['binary']), *arguments]
    return subprocess.run(argv, env=env, capture_output=True, text=True, timeout=timeout)


@contextmanager
def tls_fixture(work):
    """Fresh self-signed loopback endpoint; never uses a saved identity or account."""
    cert, key, config = [work / name for name in ('tls.pem', 'tls-key.pem', 'tls.cnf')]
    config.write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n[dn]\nCN=127.0.0.1\n[ext]\nsubjectAltName=IP:127.0.0.1\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n')
    checked(['/usr/bin/openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(key),
             '-out', str(cert), '-days', '1', '-config', str(config)])
    key.chmod(0o600)
    fingerprint = base64.urlsafe_b64encode(hashlib.sha256(ssl.PEM_cert_to_DER_cert(cert.read_text())).digest()).decode().rstrip('=')
    requests = []

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def log_message(self, *args):
            pass

        def do_GET(self):
            requests.append(('GET', self.path, dict(self.headers)))
            self.send_response(200)
            self.send_header('Content-Length', '2')
            self.end_headers()
            self.wfile.write(b'{}')

        def do_POST(self):
            requests.append(('POST', self.path, dict(self.headers)))
            body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
            mode = body.get('mode')
            self.send_response(302 if mode == 'redirect' else 200)
            if mode == 'redirect':
                self.send_header('Location', '/v1/system')
                self.send_header('Content-Length', '0')
                self.end_headers()
            elif mode == 'oversize':
                self.send_header('Transfer-Encoding', 'chunked')
                self.end_headers()
                try:
                    for _ in range(100):
                        self.wfile.write(b'100\r\n' + b'x' * 256 + b'\r\n')
                        self.wfile.flush()
                    self.wfile.write(b'0\r\n\r\n')
                except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
                    pass
            else:
                body = b'{"proof":true}'
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = None
    try:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        yield {'origin': f'https://127.0.0.1:{server.server_port}', 'fingerprint': fingerprint}
        posts = [row for row in requests if row[0] == 'POST']
        if len(posts) != 3 or any(row[1] != '/v1/networking/identity-proof' for row in posts):
            raise ValueError('TLS fixture did not receive exactly the three fixed-route proofs')
        for _, _, headers in posts:
            headers = {key.lower(): value for key, value in headers.items()}
            if headers.get('accept') != 'application/json; portico-route-proof=2' or any(
                    key in headers for key in ('authorization', 'cookie', 'referer', 'x-portico-installation-id')):
                raise ValueError('proof request violated credential-free contract')
        if any(row[1] == '/v1/system' for row in requests):
            raise ValueError('TLS proof followed a redirect')
    finally:
        if thread:
            server.shutdown()
        server.server_close()
        if thread:
            thread.join(timeout=5)


def run_suite(source, recipe, work, fixtures, device, log, products=None, pods=None):
    work.mkdir(parents=True, exist_ok=True)
    command, reason = plan(source, recipe, work, fixtures, products, pods)
    if command is None:
        log.write(f'SKIP {source}: {reason}\n')
        return {'status': 'not-run', 'reason': reason, 'assertions': 0}
    inputs = {ROOT / source, *(ROOT / item for item in recipe.get('sources', []))}
    inputs.update(Path(substitute(path, command['places'])) for path in recipe.get('resources', {}))
    if recipe['verdict']['kind'] == 'decode':
        for side in sorted((fixtures / 'audio-decode').glob('*.json')):
            inputs.add(side)
            inputs.add(side.parent / json.loads(side.read_text())['file'])
    log.write('INPUTS ' + json.dumps({str(path): hashlib.sha256(path.read_bytes()).hexdigest()
                                     for path in sorted(inputs)}) + '\n')
    built = subprocess.run(command['build'], capture_output=True, text=True, timeout=300)
    log.write(shlex.join(command['build']) + '\n' + built.stdout + built.stderr)
    if built.returncode != 0:
        return {'status': 'build-failed', 'assertions': 0}
    total, passed = 0, True

    def run_all(places):
        nonlocal total, passed
        for arguments, environment, verdict in command['runs']:
            arguments = [substitute(arg, places) for arg in arguments]
            if verdict['kind'] == 'decode':
                sides = [json.loads(path.read_text()) for path in sorted((fixtures / 'audio-decode').glob('*.json'))]
                verdict = dict(verdict, files=[row['file'] for row in sides])
            result = execute(command, arguments, environment, device, recipe.get('timeoutSeconds', 120), recipe)
            log.write(result.stdout + result.stderr)
            ok, count = outcome(result.stdout, result.returncode, verdict)
            passed, total = passed and ok, total + count
    if recipe.get('tls'):
        with tls_fixture(work) as places:
            run_all(places)
        total += 1  # Host-side fixed-route/credential/redirect assertions also passed.
    else:
        run_all({})
    return {'status': 'passed' if passed and total >= 1 else 'failed', 'assertions': total}


def receipt(revision, results, platform, device_set, artifact, root=ROOT):
    tests = []
    for source, result in sorted(results.items()):
        if result['status'] == 'passed':
            tests.append({'source': source, 'status': 'passed', 'assertions': result['assertions'], 'platform': platform,
                          'deviceOrSimulatorSet': device_set, 'resultArtifact': artifact,
                          'testSha256': hashlib.sha256((root / source).read_bytes()).hexdigest()})
    return {'revision': revision, 'tests': tests}


class Simulators:
    def __init__(self):
        self.devices = {}
        self.created = []

    def get(self, sdk, runtime=None):
        if sdk in self.devices:
            return self.devices[sdk]
        platform = 'iOS' if sdk == 'iphonesimulator' else 'tvOS'
        runtimes = json.loads(checked(['xcrun', 'simctl', 'list', 'runtimes', '--json']))['runtimes']
        available = [row for row in runtimes if row.get('isAvailable') and row['identifier'].startswith('com.apple.CoreSimulator.SimRuntime.' + platform)]
        selected = runtime or sorted(available, key=lambda row: tuple(int(n) for n in row['version'].split('.')))[-1]['identifier']
        types = json.loads(checked(['xcrun', 'simctl', 'list', 'devicetypes', '--json']))['devicetypes']
        version = next(row['version'] for row in available if row['identifier'] == selected)
        parts = [int(part) for part in version.split('.')]
        packed = sum(part << shift for part, shift in zip((parts + [0, 0])[:3], (16, 8, 0)))
        model = next(row['identifier'] for row in types
                     if row.get('productFamily') == ('iPhone' if platform == 'iOS' else 'Apple TV')
                     and row.get('minRuntimeVersion', 0) <= packed <= row.get('maxRuntimeVersion', 0xffffffff))
        device = checked(['xcrun', 'simctl', 'create', 'Portico owned tests', model, selected])
        self.created.append(device)  # Registered before boot, so boot errors also clean up.
        # Keep at most one task-owned device booted.
        for previous in self.devices.values():
            subprocess.run(['xcrun', 'simctl', 'shutdown', previous], capture_output=True, timeout=30)
        checked(['xcrun', 'simctl', 'boot', device])
        checked(['xcrun', 'simctl', 'bootstatus', device, '-b'])
        self.devices[sdk] = device
        return device

    def close(self):
        errors = []
        for device in reversed(self.created):
            for operation in ('shutdown', 'delete'):
                try:
                    result = subprocess.run(['xcrun', 'simctl', operation, device], capture_output=True, text=True, timeout=30)
                    if operation == 'delete' and result.returncode != 0:
                        errors.append(result.stderr)
                except subprocess.SubprocessError as error:
                    errors.append(str(error))
        if errors:
            raise RuntimeError('owned simulator cleanup failed: ' + '; '.join(errors))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--runtime', help='iOS runtime (default: newest available)')
    parser.add_argument('--fixtures', type=Path)
    parser.add_argument('--ios-products', type=Path)
    parser.add_argument('--pods-root', type=Path, default=ROOT / 'apps/native/ios/Pods')
    parser.add_argument('--suite', action='append', help='source stem for focused diagnostics; no complete-set receipt claim')
    parser.add_argument('--plan', action='store_true')
    args = parser.parse_args()
    recipes = json.loads(RECIPES.read_text())
    sources = owned_sources()
    if set(recipes) != set(sources):
        parser.error('recipe set does not match the owned inventory')
    sources.sort(key=lambda source: recipes[source].get('sdk') == 'appletvsimulator')
    if args.suite:
        selected = [source for source in sources if Path(source).stem in args.suite]
        if len(selected) != len(set(args.suite)):
            parser.error('unknown suite')
        sources = selected
    args.output = args.output.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    # All compiler, TLS and app scratch lives under the task output, never system /tmp.
    work = Path(tempfile.mkdtemp(prefix='scratch-', dir=args.output))
    previous_tmp = os.environ.get('TMPDIR')
    os.environ['TMPDIR'] = str(work)
    simulators = Simulators()
    results = {}
    try:
        if args.plan:
            for source in sources:
                command, reason = plan(source, recipes[source], work, args.fixtures, args.ios_products, args.pods_root)
                print(f'{source}: ' + (reason if command is None else shlex.join(command['build'])))
            return 0
        if checked(['git', 'status', '--porcelain'], cwd=ROOT):
            raise SystemExit('the checkout has uncommitted changes; a receipt names an exact commit')
        revision = checked(['git', 'rev-parse', 'HEAD'], cwd=ROOT)
        with (args.output / 'owned-apple.log').open('w', buffering=1) as log:
            for source in sources:
                recipe = recipes[source]
                try:
                    device = simulators.get(recipe.get('sdk', 'iphonesimulator'), args.runtime if recipe.get('sdk') == 'iphonesimulator' else None)
                    results[source] = run_suite(source, recipe, work / Path(source).stem, args.fixtures, device, log, args.ios_products, args.pods_root)
                except (subprocess.SubprocessError, OSError, ValueError, KeyError, TypeError, IndexError, StopIteration) as error:
                    log.write(f'ERROR {source}: {error}\n{getattr(error, "stderr", "") or ""}\n')
                    results[source] = {'status': 'error', 'reason': str(error), 'assertions': 0}
                print(f"{results[source]['status'].upper()} {source} ({results[source]['assertions']} assertions)", flush=True)
    finally:
        try:
            simulators.close()
        finally:
            try:
                shutil.rmtree(work)
            finally:
                if previous_tmp is None:
                    os.environ.pop('TMPDIR', None)
                else:
                    os.environ['TMPDIR'] = previous_tmp
    platform = checked(['sw_vers', '-productVersion'])
    document = receipt(revision, results, f'macOS {platform}; iOS/tvOS simulator', 'created disposable simulators', 'owned-apple.log')
    document['runnerSha256'] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    document['recipesSha256'] = hashlib.sha256(RECIPES.read_bytes()).hexdigest()
    for name, value in [('receipts', document), ('results', results)]:
        path = args.output / f'owned-apple-{name}.json'
        temporary = path.with_suffix('.tmp')
        temporary.write_text(json.dumps(value, indent=2) + '\n')
        temporary.replace(path)
    if args.suite:
        return 0 if all(row['status'] == 'passed' for row in results.values()) else 1
    return subprocess.run([sys.executable, '-B', str(ROOT / 'scripts/platform-tests.py'), 'owned-receipts',
                           '--receipts', str(args.output / 'owned-apple-receipts.json'), '--revision', revision]).returncode


if __name__ == '__main__':
    sys.exit(main())
