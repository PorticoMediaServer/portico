import test from 'node:test';
import assert from 'node:assert/strict';
import {readdir, readFile} from 'node:fs/promises';

test('measurement code and private reference transport cannot enter the production source graph', async () => {
  const directory = new URL('../../web/src/', import.meta.url);
  async function scan(root) {
    for (const entry of await readdir(root, {withFileTypes: true})) {
      if (entry.name === 'dev') continue; // Existing DEV-only specimens are not production entry points.
      const path = new URL(entry.name + (entry.isDirectory() ? '/' : ''), root);
      if (entry.isDirectory()) await scan(path);
      else if (/\.[cm]?[jt]sx?$/.test(entry.name)) {
        const source = await readFile(path, 'utf8');
        assert.doesNotMatch(source, /(?:from\s*|import\s*\(|import\s*)['"][^'"]*(?:tools\/fidelity|@fidelity)/, 'production code must not import tooling');
        assert.doesNotMatch(source, /data-fidelity(?:=|\s)/, 'no measurement attributes are compiled into product markup');
      }
    }
  }
  await scan(directory);
  const specimen = await readFile(new URL('../../web/src/dev/entries/accounts.tsx', import.meta.url), 'utf8');
  assert.match(specimen, /if\s*\(!import\.meta\.env\.DEV\)\s*throw/, 'existing controlled entry remains DEV fail-closed');
});
