import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerResetFilePaths} from '../src-next/screens/accounts/reset-file-paths';

test('owner reset names the file and offers every system’s documented location', () => {
  const paths = ownerResetFilePaths({fileName: 'reset-code.txt'});
  assert.equal(paths.fileName, 'reset-code.txt');
  assert.deepEqual(paths.systems.map(system => system.kind), ['linux', 'windows', 'macos', 'docker']);
  assert.equal(paths.systems.find(system => system.kind === 'linux')!.path, '/var/lib/portico/Keys/reset-code.txt');
  assert.equal(paths.systems.find(system => system.kind === 'docker')!.path, '/config/Keys/reset-code.txt');
  assert.equal(paths.systems.find(system => system.kind === 'macos')!.path, '~/Library/Application Support/Portico/Keys/reset-code.txt');
});

test('Windows help keeps its separators and service folder', () => {
  const paths = ownerResetFilePaths({fileName: 'reset-code.txt'});
  const windows = paths.systems.find(system => system.kind === 'windows')!;
  assert.equal(windows.path, '%LOCALAPPDATA%\\Portico\\Keys\\reset-code.txt');
  assert.deepEqual('detail' in windows && windows.detail, {key: 'accounts.recovery.fileWindowsService', path: 'C:\\ProgramData\\Portico\\Keys\\reset-code.txt'});
});

test('no server location was claimed when the answer carried none', () => {
  const paths = ownerResetFilePaths();
  assert.equal(paths.fileName, 'reset-code.txt');
  assert.equal(paths.systems.length, 4);
});
