import {createHash} from 'node:crypto';
import {readFileSync, writeFileSync, renameSync, existsSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
const here = fileURLToPath(import.meta.url), digest = value => createHash('sha256').update(value).digest('hex');
const patch = JSON.parse(readFileSync(new URL('./patches/react-native-webview-16.0.0-android-reader.json', import.meta.url), 'utf8'));
export function patchAndroidReader(appRoot = resolve(dirname(here), '..')) {
  const manifest = JSON.parse(readFileSync(join(appRoot, 'package.json'))), lock = JSON.parse(readFileSync(join(appRoot, 'package-lock.json')));
  if (manifest.dependencies?.['react-native-webview'] !== '16.0.0' || lock.packages?.['node_modules/react-native-webview']?.version !== '16.0.0') throw new Error('reader_webview_pin_changed');
  const require = createRequire(join(appRoot, 'package.json')), packagePath = require.resolve('react-native-webview/package.json');
  if (JSON.parse(readFileSync(packagePath)).version !== '16.0.0') throw new Error('reader_webview_pin_changed');
  const target = join(dirname(packagePath), patch.path), source = readFileSync(target, 'utf8');
  let output = source;
  if (digest(source) !== patch.afterSHA256) {
    if (digest(source) !== patch.beforeSHA256 || source.split(patch.before).length !== 2) throw new Error('reader_webview_source_changed');
    output = source.replace(patch.before, patch.after);
    if (digest(output) !== patch.afterSHA256) throw new Error('reader_webview_patch_invalid');
  }
  const helper = join(dirname(target), 'PorticoReaderAndroidAssets.java'), helperSource = readFileSync(join(appRoot, 'Native/PorticoReaderAndroidAssets.java'), 'utf8');
  if (existsSync(helper) && readFileSync(helper, 'utf8') !== helperSource) throw new Error('reader_asset_helper_changed');
  // Replacement avoids modifying another immutable dependency hardlink.
  for (const [name, value] of [[target, output], [helper, helperSource]]) {
    const temporary = name + '.portico-reader-' + process.pid; writeFileSync(temporary, value, {mode: 0o644}); renameSync(temporary, name);
  }
  return output !== source;
}
if (process.argv[1] && resolve(process.argv[1]) === here) {
  if (process.argv.length !== 2) throw new Error('reader_patch_no_arguments');
  patchAndroidReader(); console.info('Android trusted reader asset hook verified');
}
