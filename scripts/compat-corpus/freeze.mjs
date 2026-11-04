#!/usr/bin/env node
// Freeze schema/corpus evidence after release qualification. Never overwrites a snapshot.
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {parseArgs} from 'node:util';

const digest = bytes => createHash('sha256').update(bytes).digest('hex');
function files(root, prefix = '') {
  return readdirSync(join(root, prefix), {withFileTypes: true}).sort((a,b) => a.name.localeCompare(b.name))
    .flatMap(entry => entry.isDirectory() ? files(root, join(prefix, entry.name)) : [join(prefix, entry.name)]);
}
export function freeze(root, out, version, serverCommit, parserCommit) {
  if (!/^1\.\d+\.\d+$/.test(version) || ![serverCommit, parserCommit].every(pin => /^[a-f0-9]{40}$/.test(pin))) throw new Error('release version and full server/parser commit pins required');
  if (existsSync(out)) throw new Error('release snapshot already exists');
  const selected = ['server/api/portico.openapi.json', ...files(join(root, 'packages/contracts/src')).map(p => join('packages/contracts/src',p)),
    ...files(join(root,'packages/client-core/test/compat-corpus/main')).map(p => join('packages/client-core/test/compat-corpus/main',p))];
  const manifest = {version, serverCommit, parserCommit, files:{}};
  mkdirSync(out,{recursive:true});
  for (const path of selected) {
    const bytes = readFileSync(join(root,path));
    const dest = join(out,path);mkdirSync(dirname(dest),{recursive:true});copyFileSync(join(root,path),dest);
    manifest.files[path] = digest(bytes);
  }
  writeFileSync(join(out,'release.json'),JSON.stringify(manifest,null,2)+'\n');
  return manifest;
}
export function verify(out) {
  const manifest = JSON.parse(readFileSync(join(out,'release.json'),'utf8'));
  const actual = files(out).filter(p=>p!=='release.json');
  if (JSON.stringify(actual.sort())!==JSON.stringify(Object.keys(manifest.files).sort())) throw new Error('release inventory changed');
  for (const path of actual) if (digest(readFileSync(join(out,path)))!==manifest.files[path]) throw new Error(`release evidence changed: ${path}`);
  return manifest;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const {values:a}=parseArgs({options:{out:{type:'string'},version:{type:'string'},'server-commit':{type:'string'},'parser-commit':{type:'string'},verify:{type:'boolean'}}});
  if (!a.out) throw new Error('--out is required');
  if (a.verify) verify(resolve(a.out));
  else {
    const root = fileURLToPath(new URL('../../',import.meta.url));
    const status = execFileSync('git',['status','--porcelain'],{cwd:root,encoding:'utf8'}).trim();
    if (status) throw new Error('freeze only a clean, reviewed producer/parser checkout');
    const head=execFileSync('git',['rev-parse','HEAD'],{cwd:root,encoding:'utf8'}).trim();
    if (a['server-commit']!==head || a['parser-commit']!==head) throw new Error('pins must name the checked-out release producer/parser');
    freeze(root,resolve(a.out),a.version,a['server-commit'],a['parser-commit']);
    verify(resolve(a.out));
  }
  console.log('release evidence verified');
}
