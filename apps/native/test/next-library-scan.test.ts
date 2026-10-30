import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryScanReadiness} from '../src-next/shell/library-scan.ts';

test('a scan proceeds only on the ready read of the library the menu addressed', () => {
  assert.deepEqual(libraryScanReadiness({phase: 'ready', data: {library: {id: 'movies'}}}, 'movies'), {ok: true});
  assert.deepEqual(libraryScanReadiness({phase: 'ready', data: {library: {id: 'shows'}}}, 'movies'),
    {ok: false, code: 'library_management_unavailable'}, 'another library is never scanned under this action');
});

test('a failed or pending admin read reports its real code, never a false scope change', () => {
  assert.deepEqual(libraryScanReadiness({phase: 'error', data: null, error: {code: 'request_failed'}}, 'movies'), {ok: false, code: 'request_failed'});
  assert.deepEqual(libraryScanReadiness({phase: 'loading', data: null, error: null}, 'movies'), {ok: false, code: 'library_management_unavailable'});
  assert.deepEqual(libraryScanReadiness({phase: 'ready', data: null, error: {code: 'permission_changed'}}, 'movies'), {ok: false, code: 'permission_changed'});
});
