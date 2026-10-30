import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync, symlinkSync} from 'node:fs';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {auditBoundary} from '../scripts/lint-boundary.mjs';

function fixture(run: (root: string, write: (file: string, source: string) => void) => void) {
  const root = mkdtempSync(path.join(tmpdir(), 'portico-next-boundary-'));
  const write = (file: string, source: string) => {const target = path.join(root, file); mkdirSync(path.dirname(target), {recursive: true}); writeFileSync(target, source);};
  write('tsconfig.json', JSON.stringify({compilerOptions: {moduleResolution: 'bundler', module: 'esnext', baseUrl: '.', paths: {'@old/*': ['src/*'], '@next/*': ['src-next/*']}}}));
  write('src/bridge/unsafe.ts', 'export const unsafe = true;');
  write('src-next/App.ts', 'export const ready = true;');
  try {run(root, write);} finally {rmSync(root, {recursive: true, force: true});}
}

for (const [name, source] of [
  ['relative traversal', "import '../src/bridge/unsafe';"],
  ['alias', "import '@old/bridge/unsafe';"],
  ['re-export', "export * from '../src/bridge/unsafe';"],
  ['dynamic import', "void import('../src/bridge/unsafe');"],
  ['require', "require('../src/bridge/unsafe');"],
  ['require.resolve', "require.resolve('../src/bridge/unsafe');"],
  ['type import', "type T = import('../src/bridge/unsafe');"],
  ['literal concatenation', "void import('../src/' + 'bridge/unsafe');"],
] as const) test(`boundary rejects ${name} into frozen src`, () => fixture((root, write) => {
  write('src-next/App.ts', source);
  assert.match(auditBoundary({appRoot: root}).violations.join('\n'), /old src is forbidden/);
}));

test('boundary follows a helper and its re-export outside src-next', () => fixture((root, write) => {
  write('src-next/App.ts', "import '../helper';"); write('helper.ts', "export * from './src/bridge/unsafe';");
  assert.match(auditBoundary({appRoot: root}).violations.join('\n'), /old src is forbidden/);
}));
test('boundary follows native platform variants and symlinks', () => fixture((root, write) => {
  write('src-next/App.ts', "import './safe';"); write('src-next/safe.ts', 'export {};');
  write('src-next/safe.android.ts', "export * from '../src/bridge/unsafe';");
  assert.match(auditBoundary({appRoot: root}).violations.join('\n'), /old src is forbidden/);
  write('src-next/safe.android.ts', 'export {};');
  symlinkSync(path.join(root, 'src/bridge/unsafe.ts'), path.join(root, 'src-next/linked.ts'));
  assert.match(auditBoundary({appRoot: root}).violations.join('\n'), /old src is forbidden/);
}));
test('boundary fails closed on nonliteral imports and escaped gallery contexts', () => fixture((root, write) => {
  write('src-next/App.ts', "void import(variable); require.context('../src', true, /./);");
  const violations = auditBoundary({appRoot: root}).violations.join('\n');
  assert.match(violations, /statically resolvable/); assert.match(violations, /gallery context must stay/);
}));
test('boundary accepts aliases, cycles, type exports and a disjoint dev family', () => fixture((root, write) => {
  write('src-next/App.ts', "import '@next/safe';"); write('src-next/safe.ts', "export * from './App';");
  write('src-next/dev/registry.ts', "require.context('./', true, /\\.gallery\\.tsx$/);");
  assert.deepEqual(auditBoundary({appRoot: root}).violations, []);
}));

test('boundary accepts exact existing Metro images from each shell location and follows safe asset symlinks', () => fixture((root, write) => {
  write('assets/wordmark.png', 'image fixture');
  for (const header of ['PhoneHomeHeader', 'TVRail', 'DesktopHeader']) {
    write(`src-next/shell/${header}.tsx`, "const wordmark = require('../../assets/wordmark.png');");
  }
  write('src-next/App.ts', "import logo from '../assets/wordmark.png'; require('./linked.png');");
  symlinkSync(path.join(root, 'assets/wordmark.png'), path.join(root, 'src-next/linked.png'));
  assert.deepEqual(auditBoundary({appRoot: root}).violations, []);
}));

test('boundary requires the exact image file rather than declarations, source fallbacks, density variants or directories', () => fixture((root, write) => {
  write('assets/missing.png.d.ts', 'export default 1;');
  write('assets/missing.png.js', 'module.exports = 1;');
  write('assets/missing@2x.png', 'image fixture');
  write('assets/folder.png/child', 'image fixture');
  write('src-next/App.ts', "require('../assets/missing.png'); require('../assets/folder.png');");
  const violations = auditBoundary({appRoot: root}).violations;
  assert.equal(violations.length, 2);
  assert.match(violations.join('\n'), /cannot resolve ..\/assets\/missing\.png/);
  assert.match(violations.join('\n'), /cannot resolve ..\/assets\/folder\.png/);
}));

test('boundary rejects old-src assets through traversal, helpers and either symlink direction', () => fixture((root, write) => {
  write('src/assets/wordmark.png', 'frozen image fixture');
  write('assets/safe.png', 'image fixture');
  symlinkSync(path.join(root, 'src/assets/wordmark.png'), path.join(root, 'assets/linked.png'));
  symlinkSync(path.join(root, 'assets/safe.png'), path.join(root, 'src/assets/outward.png'));
  write('helper.ts', "require('./src/assets/wordmark.png');");
  write('src-next/App.ts', "require('./shell/../../src/assets/wordmark.png'); require('../assets/linked.png'); require('../src/assets/outward.png'); import '../helper';");
  const violations = auditBoundary({appRoot: root}).violations;
  assert.equal(violations.length, 4);
  assert.ok(violations.every(message => message.includes('old src is forbidden')));
}));

test('boundary keeps unresolved source modules, aliases and dynamic asset paths closed', () => fixture((root, write) => {
  write('assets/wordmark.png', 'image fixture');
  write('src-next/App.ts', "require('../assets/missing.js'); import '@next/missing'; require('../assets/' + name + '.png');");
  const violations = auditBoundary({appRoot: root}).violations;
  assert.equal(violations.length, 3);
  assert.match(violations.join('\n'), /cannot resolve ..\/assets\/missing\.js/);
  assert.match(violations.join('\n'), /cannot resolve @next\/missing/);
  assert.match(violations.join('\n'), /statically resolvable/);
}));
