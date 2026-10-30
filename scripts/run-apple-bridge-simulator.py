#!/usr/bin/env python3
"""Run the real app's account-free Apple bridge probe on a disposable simulator.

Bundle apps/native/test/apple-bridge-simulator.js with the React Native CLI first.
This checks the native bridge and private-file cleanup, not device playback or visual fidelity.
"""
import argparse
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import time
from urllib.parse import unquote, urlparse


def command(*args):
    return subprocess.check_output(['xcrun', 'simctl', *args], text=True).strip()


def snapshot_pixels(output):
    """Check actual JPEG pixels against the authored red/blue video, using the Mac SDK."""
    source = output / 'snapshot-pixels.swift'
    source.write_text('''import AppKit
import Foundation
var passed = true
for (index, path) in CommandLine.arguments.dropFirst().enumerated() {
  let image = NSBitmapImageRep(data: try Data(contentsOf: URL(fileURLWithPath: path)))!
  let x = index == 2 ? image.pixelsWide / 8 : image.pixelsWide / 2
  let y = index == 2 ? image.pixelsWide / 8 : image.pixelsHigh / 2
  let color = image.colorAt(x: x, y: y)!.usingColorSpace(.deviceRGB)!
  let red = color.redComponent, blue = color.blueComponent
  passed = passed && (index == 0 ? red > 0.7 && blue < 0.25 : blue > 0.7 && red < 0.25)
}
exit(passed ? 0 : 1)
''')
    executable = output / 'snapshot-pixels'
    subprocess.run(['xcrun', 'swiftc', str(source), '-o', str(executable)], check=True, capture_output=True, timeout=60)
    return subprocess.run([str(executable), str(output / 'red.jpg'), str(output / 'blue.jpg'), str(output / 'restored.jpg')], capture_output=True, timeout=15).returncode == 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--app', type=Path, required=True)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--video-fixture', type=Path, help='authored video for the real React snapshot probe')
    platform = parser.add_mutually_exclusive_group()
    platform.add_argument('--tvos', action='store_true')
    platform.add_argument('--ipad', action='store_true')
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    app_id = plistlib.loads((args.app / 'Info.plist').read_bytes())['CFBundleIdentifier']
    family = 'tvOS' if args.tvos else 'iOS'
    runtimes = json.loads(command('list', 'runtimes', '--json'))['runtimes']
    runtime = sorted((r for r in runtimes if r.get('isAvailable') and r['name'].startswith(family + ' ')),
                     key=lambda r: tuple(int(v) for v in r['version'].split('.')))[-1]
    types = json.loads(command('list', 'devicetypes', '--json'))['devicetypes']
    parts = [int(part) for part in runtime['version'].split('.')]
    packed = sum(part << shift for part, shift in zip((parts + [0, 0])[:3], (16, 8, 0)))
    if args.ipad:
        device_type = next(t['identifier'] for t in reversed(types) if t.get('productFamily') == 'iPad'
                           and t.get('minRuntimeVersion', 0) <= packed <= t.get('maxRuntimeVersion', 0xffffffff))
    else:
        device_type = next(t['identifier'] for t in types if t['name'] == ('Apple TV 4K (3rd generation)' if args.tvos else 'iPhone 15'))
    device = command('create', 'Portico P2-55 bridge', device_type, runtime['identifier'])
    process = None
    log_path = args.output / 'bridge-console.log'
    try:
        command('boot', device)
        command('bootstatus', device, '-b')
        command('install', device, str(args.app.resolve()))
        container = Path(command('get_app_container', device, app_id, 'data'))
        events = container / 'tmp/portico-journey.jsonl'
        events.unlink(missing_ok=True)  # A repeated owned run cannot adopt prior-container URLs/results.
        environment = dict(os.environ, SIMCTL_CHILD_PORTICO_TEST_BUNDLE_PATH=str(args.bundle.resolve()))
        if args.video_fixture:
            video = container / 'Library/Application Support/Downloads/snapshot-authored.mp4'
            video.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(args.video_fixture, video)
            environment['SIMCTL_CHILD_PORTICO_TEST_LOGIN_JSON'] = json.dumps({'snapshotUrl': video.as_uri()})
        with log_path.open('w') as log:
            process = subprocess.Popen(['xcrun', 'simctl', 'launch', '--console', device, app_id],
                                       env=environment, stdout=log, stderr=subprocess.STDOUT)
            deadline = time.monotonic() + 120
            result = None
            while time.monotonic() < deadline:
                if events.exists():
                    for line in events.read_text(errors='replace').splitlines():
                        try:
                            event = json.loads(line)
                        except ValueError:
                            continue  # a concurrently appended line may still be incomplete
                        if event.get('snapshotRestored') and args.video_fixture and not (args.output / 'restored.jpg').exists():
                            screen = args.output / 'restored.png'
                            subprocess.run(['xcrun', 'simctl', 'io', device, 'screenshot', str(screen)], check=True, capture_output=True, timeout=15)
                            subprocess.run(['sips', '-s', 'format', 'jpeg', str(screen), '--out', str(args.output / 'restored.jpg')], check=True, capture_output=True, timeout=15)
                            screen.unlink()
                        image = event.get('snapshotImage')
                        if image and image.get('name') in ('red', 'blue'):
                            source = Path(unquote(urlparse(image['image']['uri']).path))
                            if not source.resolve().is_relative_to(container.resolve()):
                                raise RuntimeError('snapshot escaped the disposable application container')
                            if source.is_file():
                                shutil.copy2(source, args.output / (image['name'] + '.jpg'))
                        if 'appleBridgeResult' in event:
                            result = event['appleBridgeResult']
                            break
                if result is not None or process.poll() is not None:
                    break
                time.sleep(.2)
        if result is None:
            raise RuntimeError('bridge probe did not report a result; see bridge-console.log')
        leftovers = list((container / 'Library/Caches/portico-cover-uploads').glob('*'))
        leftovers += list((container / 'tmp').glob('portico-invitation-*'))
        result['privateFilesRemaining'] = len(leftovers)
        passed = (result.get('failures') == 0 and result.get('assertions') and
                  all(row.get('passed') is True for row in result['assertions']) and not leftovers)
        if args.video_fixture and passed:
            result['authoredRedBluePixels'] = snapshot_pixels(args.output)
            passed = passed and result['authoredRedBluePixels']
        (args.output / 'bridge-result.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps({'passed': bool(passed), 'assertions': len(result['assertions']),
                          'privateFilesRemaining': len(leftovers), 'result': str(args.output / 'bridge-result.json')}))
        return 0 if passed else 1
    finally:
        subprocess.run(['xcrun', 'simctl', 'terminate', device, app_id], capture_output=True)
        if process is not None and process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        subprocess.run(['xcrun', 'simctl', 'shutdown', device], capture_output=True)
        subprocess.run(['xcrun', 'simctl', 'delete', device], check=True)


if __name__ == '__main__':
    raise SystemExit(main())
