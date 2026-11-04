import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {signInPolicyRows} from '@core/sign-in-policy.ts';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the nine Advanced rows bind to the configuration\'s policy rows, never to a settings form', () => {
  for (const id of Object.keys(signInPolicyRows)) {
    const binding = settingsBinding(id);
    assert.equal(binding?.form, 'signInPolicy', id);
    assert.equal(binding?.key, signInPolicyRows[id], id);
  }
});

test('a change goes to the policy PATCH with the configuration\'s revision; a conflict re-reads', () => {
  const controller = read('../src/screens/settings/controller.ts');
  assert.match(controller, /if \(isSignInPolicyRow\(row\.id\)\) \{await this\.writeSignInPolicy\(row, value\); return;\}/);
  assert.match(controller, /await patchSignInPolicy\(this\.options\.api, revision, signInPolicyWith\(values, row\.id, value\), this\.lifetime\.signal\);/);
  assert.match(controller, /if \(!signInPolicyConflict\(error\)\) throw error;\n\s+await this\.readConfiguration\(\);/);
});

test('Sign-in loads the controller too (configuration and server band), and its snapshot renews on either change', () => {
  const bindings = read('../src/screens/settings/bindings.ts');
  assert.match(bindings, /Promise\.all\(\[sections\.signIn\.load\(\), controller\.load\(section\)\]\)/);
  assert.match(bindings, /signInSnapshot\(sections\.signIn\.getSnapshot\(\), controller\.getSnapshot\(section\)\)/);
});

test('Password hashing strength stays config-only: search shows the spec\'s value when the server does not publish it', () => {
  const controller = read('../src/screens/settings/controller.ts');
  assert.match(controller, /searchConfiguration\(withSignInHashStrength\(this\.configuration, query\), query\)/);
  assert.match(read('../../packages/i18n/src/catalog/en-US/next/settings.ts'), /'next\.settings\.signin\.hashStrengthDefault': '10 \(PINs use 8\)',/);
});
