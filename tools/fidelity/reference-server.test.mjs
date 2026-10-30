import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, mkdir, writeFile, symlink, rm} from 'node:fs/promises';
import {join} from 'node:path';
import {serveReference} from './reference-server.mjs';

test('loopback reference server is read-only, rooted, type-limited and never lists files', async () => {
  assert.ok(process.env.TMPDIR, 'owned scratch required');
  const scratch = await mkdtemp(join(process.env.TMPDIR, 'reference-server-test-')), root = join(scratch, 'input');
  await mkdir(root); await writeFile(join(root, 'screen.html'), '<main>Public synthetic fixture</main>');
  await writeFile(join(root, '.hidden.html'), 'hidden'); await writeFile(join(scratch, 'outside.html'), 'outside');
  await writeFile(join(root, 'state.db'), 'not a design input'); await symlink(join(scratch, 'outside.html'), join(root, 'escape.html'));
  const server = await serveReference({root});
  try {
    assert.equal(await (await fetch(server.origin + '/screen.html')).text(), '<main>Public synthetic fixture</main>');
    for (const path of ['/', '/.hidden.html', '/state.db', '/escape.html', '/%2e%2e/outside.html', '/%5c../outside.html']) assert.equal((await fetch(server.origin + path)).status, 404);
    assert.equal((await fetch(server.origin + '/screen.html', {method: 'POST', body: 'do not write'})).status, 405);
    assert.equal(await (await fetch(server.origin + '/screen.html')).text(), '<main>Public synthetic fixture</main>');
    assert.equal(await (await fetch(server.origin + '/screen.html', {method: 'HEAD'})).text(), '');
  } finally {await server.close(); await rm(scratch, {recursive: true, force: true});}
});
