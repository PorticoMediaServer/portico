import assert from 'node:assert/strict';
import test from 'node:test';
import {redactAdminPath, runStep, flowFailed} from './flow-results.mjs';

test('admin route redaction keeps command names and removes opaque ids', () => {
  for (const command of ['check', 'scans']) {
    assert.equal(redactAdminPath(`/v1/admin/libraries/private-library/folders/private-folder/${command}`),
      `/v1/admin/libraries/:library/folders/:folder/${command}`);
  }
  assert.equal(redactAdminPath('/v1/admin/libraries/check/scans'), '/v1/admin/libraries/:library/scans');
  assert.equal(redactAdminPath('/v1/admin/libraries/id%2Fsecret/folders/id%20secret/check'),
    '/v1/admin/libraries/:library/folders/:folder/check');
});

test('steps distinguish successful, skipped and failed outcomes', async () => {
  assert.equal((await runStep('ok', async () => ({removed: true}))).status, 'pass');
  const skipped = await runStep('scan', async () => ({skipped: 'no scan control'}));
  assert.equal(skipped.status, 'skip');
  assert.equal(skipped.detail.skipped, 'no scan control');
  const failed = await runStep('remove', async () => { throw new Error('folder is still listed'); });
  assert.equal(failed.status, 'fail');
  assert.equal(failed.detail, 'folder is still listed');
});

test('only complete steps without browser errors or request failures pass a run', () => {
  const view = {steps: [{status: 'pass'}], pageErrors: [], failedRequests: []};
  assert.equal(flowFailed({viewports: [view]}), false);
  for (const failing of [
    {...view, steps: [{status: 'fail'}]},
    {...view, steps: [{status: 'skip'}]},
    {...view, pageErrors: ['uncaught exception']},
    {...view, failedRequests: ['POST /v1/admin/libraries/:library/scans failed']},
  ]) {
    assert.equal(flowFailed({viewports: [view, failing]}), true);
  }
});
