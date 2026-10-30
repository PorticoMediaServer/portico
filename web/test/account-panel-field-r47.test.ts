import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {TextField} = await import('../src/kit/controls/Fields.tsx');
const stylesheet = readFileSync(new URL('../src/kit/controls/controls.module.css', import.meta.url), 'utf8');

/** One exact rule body, so a variant claim can never be satisfied by another selector's declaration. */
function declarations(selector: string): string {
  const start = stylesheet.indexOf(`${selector} {`);
  assert.ok(start >= 0, `missing rule ${selector}`);
  return stylesheet.slice(start + selector.length, stylesheet.indexOf('}', start));
}

const render = (props: Record<string, unknown>) => renderToStaticMarkup(createElement(TextField, {variant: 'account-panel', label: 'Username', ...props}));

test('the account-panel field is the frame 46c control: 46px, 15.5px text, 13px label', () => {
  for (const declaration of ['height: 46px', 'border-radius: 11px', 'padding: 0 14px', 'var(--color-ink) 62%', 'blur(16px)']) {
    assert.ok(declarations('.field[data-variant="account-panel"] .inputShell').includes(declaration), declaration);
  }
  assert.ok(declarations('.field[data-variant="account-panel"] .input').includes('font-size: 15.5px'));
  assert.ok(declarations('.field[data-variant="account-panel"] .input').includes('caret-color: var(--color-accent-strong)'));
  const label = declarations('.field[data-variant="account-panel"] .fieldLabel');
  assert.ok(label.includes('font-size: 13px')); assert.ok(label.includes('margin-bottom: 6px'));
  assert.ok(declarations('.field[data-variant="account-panel"] .inputTrailing').includes('font-size: 13px'));
  assert.ok(declarations('.field[data-variant="account-panel"] .inputTrailing svg').includes('width: 18px'));
  assert.ok(declarations('.field[data-variant="account-panel"]:has(.input[data-filled]) .inputShell').includes('rgba(64,159,212,.13)'));
});

test('the full-page auth and compact fields keep their existing geometry', () => {
  assert.ok(declarations('.field[data-variant="auth"] .inputShell').includes('height: 54px'));
  assert.ok(declarations('.field[data-variant="auth"] .input').includes('font-size: 17px'));
  assert.ok(declarations('.field[data-variant="auth"] .fieldLabel').includes('font-size: 14px'));
  assert.ok(declarations('.field[data-variant="compact"] .inputShell').includes('height: 44px'));
  assert.ok(declarations('.field[data-variant="compact"] .input').includes('font-size: 15px'));
  assert.doesNotMatch(stylesheet, /\.field\[data-variant="account-panel"\][^{]*\{[^}]*height: (48|54|44)px/);
});

test('the account-panel code field is the frame 46b box, and the full-page code box is unchanged', () => {
  for (const declaration of ['height: 58px', 'border-radius: 12px', 'padding: 0 16px', 'var(--color-ink) 62%', 'blur(16px)']) {
    assert.ok(declarations('.field[data-variant="account-panel-code"] .inputShell').includes(declaration), declaration);
  }
  const input = declarations('.field[data-variant="account-panel-code"] .input');
  for (const declaration of ['font-size: 25px', 'font-weight: 800', 'letter-spacing: .08em', 'tabular-nums', 'caret-color: var(--color-accent-strong)']) assert.ok(input.includes(declaration), declaration);
  const label = declarations('.field[data-variant="account-panel-code"] .fieldLabel');
  assert.ok(label.includes('font-size: 13px')); assert.ok(label.includes('margin-bottom: 6px'));
  assert.ok(declarations('.field[data-variant="code"] .inputShell').includes('height: 50px'));
  assert.ok(declarations('.field[data-variant="code"] .input').includes('letter-spacing: .16em'));
  // The panel's refusal is the ordinary danger rule, not the full-page code box's record colour.
  assert.doesNotMatch(stylesheet, /\.field\[data-variant="account-panel-code"\][^{]*\{[^}]*color-record/);
  const html = renderToStaticMarkup(createElement(TextField, {variant: 'account-panel-code', label: 'Code', value: 'H4WF-9RNK', error: 'That code isn’t right, or it has expired.'}));
  assert.match(html, /data-variant="account-panel-code"/);
  assert.match(html, /aria-invalid="true"/);
  assert.match(html, /<label class="fieldLabel" for="([^"]+)">Code<\/label>/);
});

test('the account-panel field keeps the existing label, description, error, icon and disabled semantics', () => {
  const html = render({id: 'username', description: 'Your profiles and history are kept.', error: 'That code isn’t right, or it has expired.', errorIcon: 'i-warn', leadingIcon: 'i-user', 'aria-describedby': 'outer'});
  assert.match(html, /data-variant="account-panel"/);
  assert.match(html, /<label class="fieldLabel" for="username">Username<\/label>/);
  assert.match(html, /aria-describedby="outer username-help username-error"/);
  assert.match(html, /aria-invalid="true"/);
  assert.match(html, /id="username-help"[^>]*>Your profiles and history are kept\./);
  assert.match(html, /id="username-error"[^>]*>.*href="#i-warn".*That code isn’t right, or it has expired\./);
  assert.match(html, /href="#i-user"/);
});

test('the account-panel field forwards trailing content, disabled, filled and password state without inventing a default', () => {
  const trailing = render({type: 'password', trailing: createElement('button', {type: 'button'}, 'Show'), disabled: true});
  assert.match(trailing, /type="password"/);
  assert.match(trailing, /class="inputTrailing"><button type="button">Show<\/button>/);
  assert.match(trailing, /data-disabled="true"/);
  assert.match(trailing, /<input[^>]*disabled=""/);
  assert.match(render({value: 'nana_r', readOnly: true, 'data-filled': true}), /data-filled="true"/);
  assert.match(render({}), /data-variant="account-panel"/);
  const fallback = renderToStaticMarkup(createElement(TextField, {label: 'Library name'}));
  assert.match(fallback, /data-variant="field"/);
});
