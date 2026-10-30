import {execFileSync} from 'node:child_process';
import {readFile, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const packageDocument = JSON.parse(await readFile(resolve(root, 'package.json'), 'utf8'));
if (packageDocument.name !== '@portico/website') {
  throw new Error('Public-site package identity is not @portico/website');
}
const commit = execFileSync('git', ['-C', root, 'rev-parse', '--verify', 'HEAD'], {encoding: 'utf8'}).trim();
if (!/^[a-f0-9]{40}$/.test(commit)) {
  throw new Error('Public-site build requires a full lowercase Git commit');
}
await writeFile(resolve(root, 'dist/portico-static-build-identity.json'), `${JSON.stringify({
  schema: 'portico.static-build-identity.v1',
  project: packageDocument.name,
  repository: 'getportico-tv',
  commit,
}, null, 2)}\n`);
