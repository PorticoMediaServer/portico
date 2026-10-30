import {createHash} from 'node:crypto';
import {readFileSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);
const patch = JSON.parse(readFileSync(new URL('./patches/react-native-blur-4.4.1-public-api.json', import.meta.url), 'utf8'));
const sha256 = text => createHash('sha256').update(text).digest('hex');
const readJSON = path => JSON.parse(readFileSync(path, 'utf8'));

// npm postinstall and both Podfiles use this same fail-closed, offline hook.
// appRoot is injectable only for controlled-copy source tests.
export function patchAppleBlur(appRoot = resolve(dirname(scriptPath), '..')) {
  const manifest = readJSON(join(appRoot, 'package.json'));
  const lock = readJSON(join(appRoot, 'package-lock.json'));
  const selected = lock.packages?.[`node_modules/${patch.package}`];
  if (manifest.dependencies?.[patch.package] !== patch.version ||
      lock.packages?.['']?.dependencies?.[patch.package] !== patch.version ||
      selected?.version !== patch.version) {
    throw new Error(`Apple blur: manifest/lock must pin ${patch.package}@${patch.version}; review the patch before changing versions`);
  }

  const require = createRequire(join(appRoot, 'package.json'));
  const packagePath = require.resolve(`${patch.package}/package.json`);
  const installed = readJSON(packagePath);
  if (installed.name !== patch.package || installed.version !== patch.version) {
    throw new Error(`Apple blur: unsupported installed package ${installed.name}@${installed.version}; expected ${patch.package}@${patch.version}`);
  }

  // Validate every input and output before writing any file. Accept only known
  // original/patched bytes, so reruns (or interrupted multi-file writes) are safe.
  const changes = patch.files.map(file => {
    const path = join(dirname(packagePath), file.path);
    const original = readFileSync(path, 'utf8');
    const hash = sha256(original);
    if (hash === file.afterSHA256) {
      return {path, changed: false, content: original};
    }
    if (hash !== file.beforeSHA256) {
      throw new Error(`Apple blur: unexpected source SHA-256 for ${file.path}: ${hash}; refusing to patch`);
    }
    let content = file.content ?? original;
    for (const {before, after} of file.replacements ?? []) {
      if (content.split(before).length !== 2) {
        throw new Error(`Apple blur: expected exactly one patch context in ${file.path}`);
      }
      content = content.replace(before, after);
    }
    if (sha256(content) !== file.afterSHA256) {
      throw new Error(`Apple blur: unexpected patched SHA-256 for ${file.path}`);
    }
    return {path, changed: true, content};
  });
  for (const file of changes) {
    if (file.changed) {
      writeFileSync(file.path, file.content);
    }
  }
  return changes.filter(file => file.changed).length;
}

if (process.argv[1] && resolve(process.argv[1]) === scriptPath) {
  if (process.argv.length !== 2) {
    throw new Error('Apple blur: this setup hook takes no arguments');
  }
  const changed = patchAppleBlur();
  console.log(`Apple blur ${patch.version}: public-API patch verified (${changed} files changed)`);
}
