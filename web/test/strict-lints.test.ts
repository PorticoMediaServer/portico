import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {spawnSync} from 'node:child_process';
const script = (name: string) => new URL('../scripts/' + name, import.meta.url).pathname;
const canonical = (name: string) => new URL('../../packages/i18n/scripts/' + name, import.meta.url).pathname;
function fixture(t: import('node:test').TestContext) {
  const root = mkdtempSync(join(tmpdir(), 'portico-strict-')); t.after(() => rmSync(root, {recursive: true, force: true}));
  const write = (file: string, source: string) => { const p = join(root, file); mkdirSync(join(p, '..'), {recursive: true}); writeFileSync(p, source); };
  const run = (file: string, args: string[]) => spawnSync(process.execPath, [file, ...args], {encoding: 'utf8'});
  return {root, write, run};
}
test('strict design permits kit literals and rejects shell, screens, dev and platform literals', t => {
  const f = fixture(t); f.write('kit/allowed.css', '.x {color: #fff; font-family: Manrope; z-index: 20;}');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 0);
  for (const area of ['shell', 'screens', 'dev', 'platform']) {
    f.write(area + '/bad.css', '.x {color: #fff; font-family: Manrope; z-index: 20;}');
    assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 1, area);
    f.write(area + '/bad.css', '');
  }
  f.write('dev/type.css', '.x { font-size: 18px; }');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 1);
  f.write('dev/type.css', '');
  f.write('dev/bad.tsx', 'const x = <div style={{gap: 3.5, zIndex: 20}} />;');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 1);
});
test('design ignores documentation anchors while retaining real literals and inherited font keywords', t => {
  const f = fixture(t);
  f.write('screens/valid.css', '/* frame #11b */ .x {font-family: inherit;}');
  f.write('screens/valid.tsx', '/** frame #14a */ export const View = () => <div/>;');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 0);
  f.write('screens/bad.css', '.x {content: "/*"; color: #fff;} /* actual comment */');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 1);
  f.write('screens/bad.css', '');
  f.write('screens/bad.tsx', '/* frame #11b */ export const View = () => <div style={{color: "#fff"}}/>;');
  assert.equal(f.run(script('lint-design.mjs'), ['--strict', '--root', f.root]).status, 1);
});
test('dev copy has no string exemption and the next baseline is empty', t => {
  const f = fixture(t); f.write('dev/Bad.tsx', 'export const Bad = () => <button aria-label="Uncatalogued action">Literal copy</button>;');
  const args = [f.root, script('next-strings-baseline.json'), '--all', '--strict'];
  assert.equal(f.run(canonical('lint-strings.mjs'), args).status, 1);
  f.write('dev/Bad.tsx', "const t = (key: string) => key; export const Good = () => <button aria-label={t('next.controls.action')}>{t('next.controls.action')}</button>;");
  assert.equal(f.run(canonical('lint-strings.mjs'), args).status, 0);
});
test('strict raw-error lint rejects old pending paths and inline exceptions', t => {
  const f = fixture(t); f.write('screens/server/Libraries.tsx', 'const Bad = () => <p>{snapshot.error.message}</p>; // lint-raw-errors-allow');
  assert.equal(f.run(canonical('lint-raw-errors.mjs'), [f.root, '--strict']).status, 1);
  f.write('screens/server/Libraries.tsx', "const Good = () => <p>{presentError(snapshot.error)}</p>;");
  assert.equal(f.run(canonical('lint-raw-errors.mjs'), [f.root, '--strict']).status, 0);
});
test('hook rules reject conditional hooks and missing dependencies with warnings denied', t => {
  const f = fixture(t); f.write('bad.tsx', 'import {useEffect} from "react"; export function Bad({on}: {on: boolean}) { if (on) useEffect(() => {}, []); return null; }');
  const executable = new URL('../../node_modules/.bin/oxlint', import.meta.url).pathname;
  const result = spawnSync(executable, ['-c', new URL('../.oxlintrc.json', import.meta.url).pathname, '--deny-warnings', join(f.root, 'bad.tsx')], {encoding: 'utf8'});
  assert.notEqual(result.status, 0); assert.match(result.stdout + result.stderr, /rules-of-hooks/);
  f.write('bad.tsx', 'import {useEffect} from "react"; export function Bad({value}: {value: string}) { useEffect(() => { console.log(value); }, []); return null; }');
  const deps = spawnSync(executable, ['-c', new URL('../.oxlintrc.json', import.meta.url).pathname, '--deny-warnings', join(f.root, 'bad.tsx')], {encoding: 'utf8'});
  assert.notEqual(deps.status, 0); assert.match(deps.stdout + deps.stderr, /exhaustive-deps/);
});

test('glossary preserves exact current Reading/player words but rejects unrelated legacy actions', async () => {
  const {lintNextGlossary} = await import('../scripts/lint-next-glossary.mjs');
  assert.deepEqual(lintNextGlossary({'next.reader.bookmarks': 'Bookmarks', 'next.info.title': 'Playback info', 'next.tvPeople.local': 'Local account'}), []);
  assert.notEqual(lintNextGlossary({'next.unrelated': 'Bookmarks'}).length, 0);
  assert.notEqual(lintNextGlossary({'next.reader.bookmarks': 'Retry'}).length, 0);
});
