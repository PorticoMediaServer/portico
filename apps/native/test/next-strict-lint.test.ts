import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';

const app = fileURLToPath(new URL('..', import.meta.url));
const run = (args: string[]) => spawnSync(process.execPath, args, {cwd: app, encoding: 'utf8'});
test('next strings use an empty baseline and strict raw errors reject legacy filenames and inline exemptions', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'portico-next-strings-'));
  try {
    mkdirSync(path.join(root, 'screens/server'), {recursive: true});
    writeFileSync(path.join(root, 'strings.json'), '{}');
    const source = path.join(root, 'screens/server/Libraries.tsx');
    writeFileSync(source, 'export const Screen = () => <Text>Uncatalogued message</Text>;');
    const strings = run(['../../packages/i18n/scripts/lint-strings.mjs', root, path.join(root, 'strings.json'), '--all', '--strict']);
    assert.equal(strings.status, 1); assert.match(strings.stdout + strings.stderr, /Uncatalogued message/);
    writeFileSync(source, 'export const Screen = () => <Text>{model.error.message}</Text>; // lint-raw-errors-allow\n');
    const errors = run(['../../packages/i18n/scripts/lint-raw-errors.mjs', root, '--strict']);
    assert.equal(errors.status, 1); assert.match(errors.stdout + errors.stderr, /raw error.message/);
    writeFileSync(source, "export const Screen = () => <Text>{i18n.t('next.platform.kitTitle')}</Text>;\n");
    assert.equal(run(['../../packages/i18n/scripts/lint-strings.mjs', root, path.join(root, 'strings.json'), '--all', '--strict']).status, 0);
    assert.equal(run(['../../packages/i18n/scripts/lint-raw-errors.mjs', root, '--strict']).status, 0);
  } finally {rmSync(root, {recursive: true, force: true});}
});
test('the real hooks checker rejects conditional hooks and incomplete dependency lists', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'portico-next-hooks-'));
  try {
    const source = path.join(root, 'Screen.tsx');
    writeFileSync(source, "import {useState, useEffect} from 'react';\nexport function Screen({active, value}) { if (active) useState(0); useEffect(() => { console.log(value); }, []); return null; }\n");
    const args = ['node_modules/eslint/bin/eslint.js', '--no-eslintrc', '-c', '.eslintrc.hooks.cjs', '--no-ignore', source];
    const result = run(args);
    assert.equal(result.status, 1); assert.match(result.stdout + result.stderr, /react-hooks\/rules-of-hooks/); assert.match(result.stdout + result.stderr, /react-hooks\/exhaustive-deps/);
    writeFileSync(source, "import {useState, useEffect} from 'react';\nexport function Screen({value}) { useState(0); useEffect(() => { console.log(value); }, [value]); return null; }\n");
    assert.equal(run(args).status, 0);
  } finally {rmSync(root, {recursive: true, force: true});}
});
