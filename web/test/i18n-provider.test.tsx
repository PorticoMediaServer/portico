import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {createI18n, defaultI18n, deviceRegion, resolveRegion, type I18n} from '@i18n';
import type {PreferenceField, PreferenceSnapshot} from '@core/preferences.ts';
import {serviceI18n, serviceText, setServiceI18n} from '@core/presentation/service-text.ts';
import {I18nContext, I18nProvider, applyShellI18n, resolveShellI18n, useI18n} from '../src/shell/i18n.tsx';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, textOf} from './helpers/interaction-harness.mjs';

const REGION_DEFAULTS = {
  'region.locale': 'en-US', 'region.hourCycle': 'auto', 'region.formats': 'auto',
  'region.dateOrder': 'auto', 'region.firstDay': 'auto', 'region.sizeUnits': 'binary',
} as const;

function makePreferences(overrides: Partial<Record<keyof typeof REGION_DEFAULTS, string>> = {}): PreferenceSnapshot {
  const effective = {...REGION_DEFAULTS, ...overrides};
  const fields: readonly PreferenceField[] = Object.keys(REGION_DEFAULTS).map(key => ({
    key, type: key === 'region.locale' ? 'locale' as const : 'string' as const,
    default: REGION_DEFAULTS[key as keyof typeof REGION_DEFAULTS],
    scopes: ['profile-server' as const], group: 'region', labelKey: `pref.label.${key}`,
  }));
  return {
    deviceClass: 'web', registry: {revision: '1', fields}, documents: [],
    effective, effectiveSource: {}, clampedFields: [], registryRevision: '1',
  };
}

/** Context-aware React shim for interaction-harness tests that exercise Provider + useContext + useEffect live. */
function createContextRenderer() {
  const base = createRenderer();
  const react: any = {
    ...base.react,
    createContext: (initial: unknown) => {
      const ctx: any = {value: initial};
      ctx.Provider = ({value, children}: {value: unknown; children: unknown}) => {
        ctx.value = value;
        return children;
      };
      return ctx;
    },
    useContext: (ctx: {value: unknown}) => ctx.value,
  };
  react.default = react;
  return {...base, react};
}

test('I18nContext defaults to defaultI18n when rendered outside I18nProvider', () => {
  const observed: I18n[] = [];
  function Reader() {
    const i18n = useI18n();
    observed.push(i18n);
    return createElement('span', null, i18n.t('saved.tab.favorites'));
  }
  const html = renderToStaticMarkup(createElement(Reader));
  assert.equal(observed[0], defaultI18n);
  assert.equal(html, `<span>${defaultI18n.t('saved.tab.favorites')}</span>`);
  assert.ok(I18nContext);
});

test('resolveShellI18n resolves locale tags, region preferences, and preference snapshots', () => {
  const device = {locales: ['en-US']};
  assert.equal(resolveShellI18n('en-GB', device).locale, 'en-GB');
  assert.equal(resolveShellI18n('en-GB', device).t('saved.tab.favorites'), 'Favourites');
  assert.equal(resolveShellI18n({locale: 'en-CA', hourCycle: 'h23'}, device).region.hour12, false);
  assert.equal(resolveShellI18n(makePreferences({'region.locale': 'en-GB', 'region.sizeUnits': 'decimal'}), device).region.sizeUnits, 'decimal');
  assert.equal(resolveShellI18n(undefined, device).locale, 'en-US');
});

test('I18nProvider renders localized copy through real React context without needing a reload', () => {
  function Label() {
    const i18n = useI18n();
    return createElement('span', {'data-locale': i18n.locale}, i18n.t('saved.tab.favorites'));
  }
  const us = renderToStaticMarkup(createElement(I18nProvider, {locale: 'en-US', device: {locales: ['en-US']}}, createElement(Label)));
  const gb = renderToStaticMarkup(createElement(I18nProvider, {locale: 'en-GB', device: {locales: ['en-US']}}, createElement(Label)));
  assert.equal(us, '<span data-locale="en-US">Favorites</span>');
  assert.equal(gb, '<span data-locale="en-GB">Favourites</span>');
});

test('I18nProvider live locale switching updates document.documentElement.lang, setServiceI18n, api.setAppLanguage, and useI18n consumers', async () => {
  const beforeService = serviceI18n();
  const oldDocument = globalThis.document;
  const fakeDoc = {documentElement: {lang: ''}};
  globalThis.document = fakeDoc as unknown as Document;
  const h = createContextRenderer();
  try {
    const mod = await componentModule(new URL('../src/shell/i18n.tsx', import.meta.url), {
      react: h.react,
      '@i18n': {createI18n, defaultI18n, deviceRegion, resolveRegion},
      '@core/presentation/service-text.ts': {setServiceI18n},
    });
    const apiLanguages: string[] = [];
    const api = {setAppLanguage: (lang: string) => { apiLanguages.push(lang); }};
    const seenI18n: I18n[] = [];
    function Consumer() {
      const i18n: I18n = mod.useI18n();
      seenI18n.push(i18n);
      return h.react.createElement('p', {'data-locale': i18n.locale}, i18n.t('saved.tab.favorites'));
    }

    const device = {locales: ['en-US']};
    const first = h.render(() => h.react.createElement(mod.I18nProvider, {
      preferences: makePreferences({'region.locale': 'en-US'}), api, device,
    }, h.react.createElement(Consumer)));
    assert.equal(textOf(first), 'Favorites');
    assert.equal(fakeDoc.documentElement.lang, 'en-US');
    assert.equal(serviceI18n().locale, 'en-US');
    assert.equal(serviceText('saved.tab.favorites'), 'Favorites');
    assert.deepEqual(apiLanguages, ['en-US']);

    // Unrelated preference re-render with identical region.* keeps memoized I18n & avoids redundant effect runs.
    h.render(() => h.react.createElement(mod.I18nProvider, {
      preferences: makePreferences({'region.locale': 'en-US'}), api, device,
    }, h.react.createElement(Consumer)));
    assert.equal(seenI18n[0], seenI18n[1]);
    assert.deepEqual(apiLanguages, ['en-US']);

    // Switching locale to en-GB updates lang, service-text, API header, and consumer text reactively.
    const second = h.render(() => h.react.createElement(mod.I18nProvider, {
      preferences: makePreferences({'region.locale': 'en-GB'}), api, device,
    }, h.react.createElement(Consumer)));
    assert.equal(textOf(second), 'Favourites');
    assert.notEqual(seenI18n[2], seenI18n[1]);
    assert.equal(fakeDoc.documentElement.lang, 'en-GB');
    assert.equal(serviceI18n().locale, 'en-GB');
    assert.equal(serviceText('saved.tab.favorites'), 'Favourites');
    assert.deepEqual(apiLanguages, ['en-US', 'en-GB']);

    // Switching back via explicit locale prop updates cleanly again.
    const third = h.render(() => h.react.createElement(mod.I18nProvider, {
      locale: 'en-CA', api, device,
    }, h.react.createElement(Consumer)));
    assert.equal(textOf(third), 'Favourites');
    assert.equal(fakeDoc.documentElement.lang, 'en-CA');
    assert.equal(serviceI18n().locale, 'en-CA');
    assert.deepEqual(apiLanguages, ['en-US', 'en-GB', 'en-CA']);

    h.dispose();
  } finally {
    setServiceI18n(beforeService);
    globalThis.document = oldDocument;
  }
});

test('applyShellI18n updates document lang, service i18n, and api.setAppLanguage', () => {
  const beforeService = serviceI18n();
  const oldDocument = globalThis.document;
  const fakeDoc = {documentElement: {lang: ''}};
  globalThis.document = fakeDoc as unknown as Document;
  try {
    const called: string[] = [];
    const gb = resolveShellI18n('en-GB', {locales: ['en-US']});
    applyShellI18n(gb, {setAppLanguage: lang => called.push(lang)});
    assert.equal(fakeDoc.documentElement.lang, 'en-GB');
    assert.equal(serviceI18n(), gb);
    assert.deepEqual(called, ['en-GB']);
  } finally {
    setServiceI18n(beforeService);
    globalThis.document = oldDocument;
  }
});

test('App keeps I18nProvider unconditionally mounted and bootstrap propagates live locale updates without restart()', () => {
  const appSrc = readFileSync(new URL('../src/App.tsx', import.meta.url), 'utf8');
  assert.match(appSrc, /<I18nProvider\b/);
  assert.doesNotMatch(appSrc, /<I18nProvider[^>]*key=/);
  assert.doesNotMatch(appSrc, /\?\s*<I18nProvider/);

  const bootstrapSrc = readFileSync(new URL('../src/shell/bootstrap.ts', import.meta.url), 'utf8');
  assert.match(bootstrapSrc, /applyRegionLanguage\(next\)/);
  assert.match(bootstrapSrc, /setServiceI18n\(language\)/);
  assert.match(bootstrapSrc, /api\.setAppLanguage\?\.\(language\.locale\)/);
  assert.doesNotMatch(bootstrapSrc, /if \(key !== regionKey\) \{ regionKey = key; restart\(\); \}/);
});
