import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerResetFilePaths} from '../src-next/screens/accounts/reset-file-paths';

test('owner reset keeps the actual server path and shows only the other systems', () => {
  const paths = ownerResetFilePaths({systemKind: 'docker', dataDirectory: '/mounted/data', fileDirectory: '/mounted/data/', fileName: 'reset-code.txt', retryAfterSeconds: 0});
  assert.equal(paths.system, 'Docker');
  assert.equal(paths.path, '/mounted/data/reset-code.txt');
  assert.deepEqual(paths.alternatives.map(system => system.kind), ['linux', 'windows', 'macos']);
  assert.equal(paths.alternatives.find(system => system.kind === 'linux')!.path, '/var/lib/portico/reset-code.txt');
});

test('Windows returned directories keep Windows separators and never borrow a default', () => {
  const paths = ownerResetFilePaths({systemKind: 'windows', dataDirectory: 'D:\\Portico', fileDirectory: 'D:\\Portico\\Keys\\', fileName: 'reset-code.txt', retryAfterSeconds: 0});
  assert.equal(paths.path, 'D:\\Portico\\Keys\\reset-code.txt');
  assert.equal(paths.alternatives.find(system => system.kind === 'docker')!.path, '/config/reset-code.txt');
  assert.equal(paths.alternatives.find(system => system.kind === 'macos')!.path, '~/Library/Application Support/Portico/reset-code.txt');
});

test('unknown system help uses Linux and binding current installation defaults', () => {
  const paths = ownerResetFilePaths();
  assert.equal(paths.system, 'Linux');
  assert.equal(paths.path, '/var/lib/portico/reset-code.txt');
  assert.deepEqual(paths.alternatives.map(system => system.kind), ['windows', 'macos', 'docker']);
  const windows = paths.alternatives.find(system => system.kind === 'windows')!;
  assert.equal(windows.path, '%LOCALAPPDATA%\\Portico\\reset-code.txt');
  assert.deepEqual('detail' in windows && windows.detail, {key: 'accounts.recovery.fileWindowsService', path: 'C:\\ProgramData\\Portico\\reset-code.txt'});
});
