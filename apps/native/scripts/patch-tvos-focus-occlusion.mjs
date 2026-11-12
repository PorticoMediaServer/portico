import {createHash} from 'node:crypto';
import {readFileSync, renameSync, rmSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const script = fileURLToPath(import.meta.url);
const patch = JSON.parse(readFileSync(new URL('./patches/react-native-tvos-0.83.10-1-transparent-focus.json', import.meta.url), 'utf8'));
const readJSON = path => JSON.parse(readFileSync(path, 'utf8'));
const hash = text => createHash('sha256').update(text).digest('hex');

// tvOS treats every view drawn above an item as covering it, even an empty pass-through layer, and then offers no move to
// that item (the side rail and pushed pages under the shell's playback host). The patch makes a plain view with no
// background that cannot take focus transparent to focus. npm postinstall and the tvOS Podfile run it; it fails closed on
// any other react-native-tvos version or source. appRoot is injectable only for controlled-copy tests.
export function patchTVOSFocusOcclusion(appRoot = resolve(dirname(script), '..')) {
  const manifest = readJSON(join(appRoot, 'package.json'));
  const lock = readJSON(join(appRoot, 'package-lock.json'));
  const selected = lock.packages?.[`node_modules/${patch.package}`];
  if (manifest.dependencies?.[patch.package] !== patch.version || lock.packages?.['']?.dependencies?.[patch.package] !== patch.version ||
      selected?.name !== patch.installedName || selected?.version !== patch.installedVersion) {
    throw new Error('tvOS focus: review the transparent-focus patch before changing the pinned react-native-tvos version');
  }
  const require = createRequire(join(appRoot, 'package.json'));
  const packagePath = require.resolve(`${patch.package}/package.json`);
  const installed = readJSON(packagePath);
  if (installed.name !== patch.installedName || installed.version !== patch.installedVersion) {
    throw new Error(`tvOS focus: installed ${installed.name}@${installed.version} is not the reviewed version`);
  }
  const changes = patch.files.map(file => {
    const path = join(dirname(packagePath), file.path);
    const original = readFileSync(path, 'utf8');
    if (hash(original) === file.afterSHA256) return {path, content: original, changed: false};
    if (hash(original) !== file.beforeSHA256) throw new Error(`tvOS focus: unexpected source bytes in ${file.path}`);
    let content = original;
    for (const {before, after} of file.replacements) {
      if (content.split(before).length !== 2) throw new Error(`tvOS focus: ambiguous source hook in ${file.path}`);
      content = content.replace(before, after);
    }
    if (hash(content) !== file.afterSHA256) throw new Error(`tvOS focus: unexpected patched source in ${file.path}`);
    return {path, content, changed: true};
  });
  for (const file of changes) {
    if (!file.changed) continue;
    // Replace rather than write in place: an installed dependency file may be a hard link shared with another tree.
    const temporary = `${file.path}.portico-focus-${process.pid}.tmp`;
    try {writeFileSync(temporary, file.content); renameSync(temporary, file.path);}
    finally {rmSync(temporary, {force: true});}
  }
  return changes.filter(file => file.changed).length;
}

if (process.argv[1] && resolve(process.argv[1]) === script) {
  if (process.argv.length !== 2) throw new Error('tvOS focus: this setup hook takes no arguments');
  console.log(`tvOS focus: transparent-focus patch verified (${patchTVOSFocusOcclusion()} files changed)`);
}
