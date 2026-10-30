import {createHash} from 'node:crypto';
import {readFileSync, writeFileSync, renameSync, rmSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const script = fileURLToPath(import.meta.url);
const patch = JSON.parse(readFileSync(new URL('./patches/react-native-webview-16.0.0-reader-origin.json', import.meta.url), 'utf8'));
const readJSON = path => JSON.parse(readFileSync(path, 'utf8'));
const hash = text => createHash('sha256').update(text).digest('hex');

// The iOS Podfile always invokes this, including installs that skipped npm
// lifecycle scripts. appRoot injection is solely for controlled source tests.
export function patchAppleReaderWebView(appRoot = resolve(dirname(script), '..')) {
  const manifest = readJSON(join(appRoot, 'package.json'));
  const lock = readJSON(join(appRoot, 'package-lock.json'));
  if (manifest.dependencies?.[patch.package] !== patch.version ||
      lock.packages?.['']?.dependencies?.[patch.package] !== patch.version ||
      lock.packages?.[`node_modules/${patch.package}`]?.version !== patch.version) {
    throw new Error('Apple Read: review the local-origin hook before changing the pinned WebView version');
  }
  const require = createRequire(join(appRoot, 'package.json'));
  const packagePath = require.resolve(`${patch.package}/package.json`);
  const installed = readJSON(packagePath);
  if (installed.name !== patch.package || installed.version !== patch.version) {
    throw new Error('Apple Read: the installed WebView does not match the reviewed version');
  }
  const changes = patch.files.map(file => {
    const path = join(dirname(packagePath), file.path);
    const original = readFileSync(path, 'utf8');
    if (hash(original) === file.afterSHA256) return {path, content: original, changed: false};
    if (hash(original) !== file.beforeSHA256) throw new Error(`Apple Read: unexpected source bytes in ${file.path}`);
    let content = original;
    for (const {before, after} of file.replacements) {
      if (content.split(before).length !== 2) throw new Error(`Apple Read: ambiguous source hook in ${file.path}`);
      content = content.replace(before, after);
    }
    if (hash(content) !== file.afterSHA256) throw new Error(`Apple Read: unexpected patched source in ${file.path}`);
    return {path, content, changed: true};
  });
  for (const file of changes) {
    if (!file.changed) continue;
    const temporary = `${file.path}.portico-reader-${process.pid}.tmp`;
    try {writeFileSync(temporary, file.content); renameSync(temporary, file.path);}
    finally {rmSync(temporary, {force: true});}
  }
  return changes.filter(file => file.changed).length;
}

if (process.argv[1] && resolve(process.argv[1]) === script) {
  if (process.argv.length !== 2) throw new Error('Apple Read: this setup hook takes no arguments');
  console.log(`Apple Read ${patch.version}: local-origin hook verified (${patchAppleReaderWebView()} files changed)`);
}
