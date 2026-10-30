import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {loadCredentials, redactor} from './creds.mjs';

// Synthetic secrets only; these assertions run at a checkpoint, never against an account.
test('redaction removes every literal occurrence, including regexp punctuation', () => {
  const secret = 'fixture.$[secret]';
  const redact = redactor(secret);
  assert.equal(redact(`before ${secret} between ${secret} after`),
    'before <redacted> between <redacted> after');
  assert.equal(redact('unrelated diagnostic'), 'unrelated diagnostic');
});

test('malformed private JSON never contributes its contents to the diagnostic', () => {
  const out = mkdtempSync(join(tmpdir(), 'portico-smoke-creds-'));
  try {
    const path = join(out, 'credentials.json');
    const secret = 'fixture-secret-that-must-not-be-logged';
    writeFileSync(path, `{"password":"${secret}",`, {mode: 0o600});
    assert.throws(() => loadCredentials(path), error => {
      assert.equal(error.message, `cannot read credentials at ${path}: parse error`);
      assert.equal(error.message.includes(secret), false);
      return true;
    });
  } finally {
    rmSync(out, {recursive: true, force: true});
  }
});
