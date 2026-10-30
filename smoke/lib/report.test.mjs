import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createReport, assert as requireCheck} from './report.mjs';
import {buildStability} from './build.mjs';

// Checkpoint-only regression coverage; no browser, network or credentials.
test('a deployment-pin failure remains failed in the serialized checkpoint report', async () => {
  const out = mkdtempSync(join(tmpdir(), 'portico-smoke-report-'));
  try {
    const report = createReport({surface: 'hosted', origin: 'https://example.invalid', out});
    await report.check('boot', async () => ({detail: 'report fixture'}));
    report.blocked('optional-fixture', 'fixture unavailable');
    report.note('build-pin', 'deployment pin mismatch');
    await report.check('build-pin-valid', async () => {
      requireCheck(false, 'deployment does not match the requested build pin');
    });
    const result = report.write();
    const saved = JSON.parse(readFileSync(join(out, 'report.json'), 'utf8'));
    assert.equal(result.verdict, 'fail');
    assert.equal(saved.verdict, 'fail');
    assert.deepEqual(saved.counts, {pass: 1, fail: 1, blocked: 1});
    const pin = saved.checks.find(check => check.id === 'build-pin-valid');
    assert.equal(pin.status, 'fail');
    assert.equal(pin.required, true);
    assert.equal(pin.detail, 'deployment does not match the requested build pin');
  } finally {
    rmSync(out, {recursive: true, force: true});
  }
});

for (const [name, end] of [
  ['unreadable', null],
  ['replaced server', {version: 'server-one', id: 'server-two', build: {commit: 'a'.repeat(40)}}],
  ['changed static build', {version: 'server-one', id: 'server-one', build: {commit: 'b'.repeat(40)}}],
]) {
  test(`${name} deployment is saved as a required checkpoint failure with both stamps`, async () => {
    const out = mkdtempSync(join(tmpdir(), 'portico-smoke-stability-'));
    try {
      const start = {version: 'server-one', id: 'server-one', build: {commit: 'a'.repeat(40)}};
      const report = createReport({surface: 'web', origin: 'https://example.invalid', out});
      report.buildInfo = {start, end};
      const stability = buildStability(start, end);
      await report.check('build-stability', async () => {
        requireCheck(stability.valid, stability.reason, {start, end});
      });
      report.write();
      const saved = JSON.parse(readFileSync(join(out, 'report.json'), 'utf8'));
      assert.equal(saved.verdict, 'fail');
      assert.deepEqual(saved.build, {start, end});
      assert.equal(saved.checks[0].required, true);
      assert.equal(saved.checks[0].status, 'fail');
      assert.equal(saved.checks[0].detail, stability.reason);
      assert.deepEqual(saved.checks[0].evidence, {start, end});
    } finally {
      rmSync(out, {recursive: true, force: true});
    }
  });
}

test('a long checkpoint error stays failed while its saved diagnostic is bounded', async () => {
  const out = mkdtempSync(join(tmpdir(), 'portico-smoke-error-'));
  try {
    const report = createReport({surface: 'web', origin: 'https://example.invalid', out});
    const message = 'checkpoint fixture '.repeat(100);
    const evidence = {step: 'fixture-step', path: '/fixture'};
    await report.check('required-flow', async () => {
      requireCheck(false, message, evidence);
    });
    report.write();
    const saved = JSON.parse(readFileSync(join(out, 'report.json'), 'utf8'));
    assert.equal(saved.verdict, 'fail');
    assert.equal(saved.checks[0].required, true);
    assert.equal(saved.checks[0].detail, message.slice(0, 1200));
    assert.equal(saved.checks[0].detail.length, 1200);
    assert.deepEqual(saved.checks[0].evidence, evidence);
  } finally {
    rmSync(out, {recursive: true, force: true});
  }
});
