import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const bootstrap = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');

test('setting or removing a PIN on the profile that is open here does not end the shell', () => {
  // The server already sends every OTHER device on the profile back to the chooser when a PIN is set
  // (db.UpdateProfile leaves the saving session alone), so a comparison here can only end this device's own shell.
  assert.doesNotMatch(bootstrap, /nextProfile\.pinRequired !== profile\.pinRequired/);
});

test('every read of profile pictures treats "not on this server" as no pictures', () => {
  const reads = bootstrap.match(/identities\.avatars\([^)]*\)(\.catch\(noPictures\))?/g) ?? [];
  assert.equal(reads.length, 3);
  assert.deepEqual(reads.filter(read => !read.endsWith('.catch(noPictures)')), []);
});
