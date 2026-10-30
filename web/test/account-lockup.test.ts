import assert from 'node:assert/strict';
import test from 'node:test';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {createI18n, resolveRegion, supportedLocales} from '../../packages/i18n/src/index.ts';
import {AuthStage} from '../src/kit/accounts/AuthStage.tsx';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

test('the web lockup uses its catalogue label in every supported locale without renaming Account settings', () => {
  for (const locale of supportedLocales) {
    const {t} = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(t('web.brand.player'), 'Web Player', locale);
    assert.equal(t('next.shell.account'), 'Account', locale);
  }
  const html = renderToStaticMarkup(createElement(AuthStage, {service: true, children: 'Content'}));
  assert.match(html, /<header[^>]*><img[^>]*alt="Portico"[^>]*\/><span>Web Player<\/span><\/header>/);
  assert.doesNotMatch(renderToStaticMarkup(createElement(AuthStage, {children: 'Content'})), /Web Player/);
});

test('the shared password field keeps its value, autocomplete and eye action when revealing it', async () => {
  const h = createRenderer(), react = {...h.react, useId: () => 'password-check'};
  const fields = await componentModule(new URL('../src/kit/controls/Fields.tsx', import.meta.url), {
    react, '../Icon': {Icon: 'Icon'}, './controls.module.css': {default: new Proxy({}, {get: (_target, key) => String(key)})},
  });
  const {AuthPasswordField} = await componentModule(new URL('../src/kit/accounts/AuthStage.tsx', import.meta.url), {
    react, '@i18n': {defaultI18n: createI18n()}, '../controls/Fields': fields,
  });
  const render = () => tree(h.render(() => createElement(AuthPasswordField, {label: 'Password', value: 'visibility-check', autoComplete: 'current-password', onChange() {}})));
  try {
    let shown = render(), input = shown.find(node => node.type === 'input');
    assert.equal(input?.props.type, 'password');
    assert.equal(input?.props.autoComplete, 'current-password');
    shown.find(node => node.type === 'button' && node.props['aria-label'] === 'Show password')!.props.onClick();
    shown = render(); input = shown.find(node => node.type === 'input');
    assert.equal(input?.props.type, 'text');
    assert.equal(input?.props.value, 'visibility-check');
    shown.find(node => node.type === 'button' && node.props['aria-label'] === 'Hide password')!.props.onClick();
    assert.equal(render().find(node => node.type === 'input')?.props.type, 'password');
  } finally {h.dispose();}
});
