import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {profileArtColors} from '@design';
import {transformTsx} from '../transform-tsx.mjs';
import {createArtworkRuntime} from '../../src/kit/artwork/source.ts';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {CircleArt, CoverMosaic} = await import('../../src/kit/artwork/CircleArt.tsx');
const {ProfileFace, ProfilePicker} = await import('../../src/kit/artwork/ProfilePicker.tsx');
const {ChannelMark} = await import('../../src/kit/artwork/ChannelMark.tsx');

test('ChannelMark uses only the actual seed tint while retaining text and its logo layer', () => {
  const runtime = createArtworkRuntime({scopeKey: 'channel-mark', load: async () => { throw new Error('SSR must not request images'); }, classify: () => ({retryable: false})});
  const mark = {sourceId: 'other-source', id: 'other-id', text: 'N24', seed: 'source:northwind-news-24'};
  const source = runtime.source({url: '/actual-logo', cacheKey: 'logo'});
  try {
    const plain = renderToStaticMarkup(createElement(ChannelMark, {mark}));
    assert.match(plain, /--channel-tint:#28665d/);
    assert.match(plain, />N24</);
    assert.doesNotMatch(plain, /channelLogo/);
    const logo = renderToStaticMarkup(createElement(ChannelMark, {mark, source, alt: 'Actual channel'}));
    assert.match(logo, /--channel-tint:#28665d/);
    assert.match(logo, /channelLogo/);
    assert.match(logo, /aria-label="Actual channel"/);
    assert.equal(mark.seed, 'source:northwind-news-24');
  } finally {runtime.dispose();}
});

test('the actual mosaic bounds cover count and preserves fallback, shape and caller clipping geometry', () => {
  const runtime = createArtworkRuntime({scopeKey: 'renderer-test', load: async () => { throw new Error('SSR must not request images'); }, classify: () => ({retryable: false})});
  const covers = Array.from({length: 5}, (_, index) => runtime.source({url: `/reference-${index}`, cacheKey: String(index)}));
  for (let count = 0; count <= 5; count++) {
    const fallbacks: number[] = [];
    const html = renderToStaticMarkup(createElement(CoverMosaic, {covers: covers.slice(0, count), alt: 'Saved covers', shape: 'poster', visible: false, width: 48, height: 72, style: {borderRadius: 8}, renderFallback: index => { fallbacks.push(index); return createElement('b', null, index); }}));
    assert.equal((html.match(/data-artwork-status="empty"/g) ?? []).length, Math.min(count, 4) || 1);
    assert.deepEqual(fallbacks, Array.from({length: Math.min(count, 4) || 1}, (_, index) => index));
    assert.match(html, /role="img" aria-label="Saved covers"/);
    assert.match(html, /data-cover-shape="poster"/);
    assert.match(html, /width:48px;height:72px;border-radius:8px/);
    assert.doesNotMatch(html, /<img|<button/);
  }
  runtime.dispose();
});

test('ProfileFace uses the canonical semantic base unless the caller explicitly supplies a tint', () => {
  for (const art of Object.keys(profileArtColors) as (keyof typeof profileArtColors)[]) {
    const html = renderToStaticMarkup(createElement(ProfileFace, {initial: 'D', art}));
    assert.ok(html.includes(`--profile-colour:${profileArtColors[art]}`));
  }
  const html = renderToStaticMarkup(createElement(ProfileFace, {initial: 'D', art: 'blue', tint: '#2f86bb', size: 90, locked: true}));
  assert.match(html, /--profile-colour:#2f86bb/);
  assert.match(html, /--profile-size:90px/);
  assert.match(html, /class="lockBadge"/);
  assert.equal(profileArtColors.blue, '#28587a');
  assert.equal(profileArtColors.gold, '#796120');
});

test('picker tint mapping receives original profiles and leaves unmapped semantic colours and eligibility intact', () => {
  const profiles = Object.freeze([
    Object.freeze({id: 'reference-main', name: 'Reference main', eligible: true, art: 'blue', primary: true}),
    Object.freeze({id: 'reference-unshared', name: 'Reference unshared', eligible: false, art: 'gold', unavailableReason: 'membership_required' as const}),
  ]);
  const seen: unknown[] = [];
  const html = renderToStaticMarkup(createElement(ProfilePicker, {profiles, onSelect() {}, getProfileTint: profile => { seen.push(profile); return profile.id === 'reference-main' ? '#2f86bb' : undefined; }}));
  assert.equal(seen[0], profiles[0]);
  assert.equal(seen[1], profiles[1]);
  assert.match(html, /--profile-colour:#2f86bb/);
  assert.match(html, /--profile-colour:#796120/);
  assert.match(html, /disabled=""/);
  assert.match(html, /Not shared/);
  assert.deepEqual(profiles.map(profile => profile.art), ['blue', 'gold']);
});


test('single-cover presentation inputs pass through CircleCard without changing omitted defaults or person art', async () => {
  const {CircleCard} = await import('../../src/kit/cards/MediaCards.tsx');
  const runtime = createArtworkRuntime({scopeKey: 'circle-presentation', load: async () => { throw new Error('SSR must not request images'); }, classify: () => ({retryable: false})});
  const covers = [runtime.source({url: '/real-cover', cacheKey: 'cover'})];
  const props = {covers, model: {title: 'Supplied artist', caption: []}, context: 'search' as const, onNavigate() {}};
  const html = renderToStaticMarkup(createElement(CircleCard, {...props, coverZoom: 1.75, coverTransformOrigin: '52% 62%'}));
  assert.match(html, /--circle-cover-zoom:1.75/);
  assert.match(html, /--circle-cover-origin:52% 62%/);
  assert.doesNotMatch(html, /coverTransformOrigin=|coverZoom=|object-position:/);
  for (const element of [createElement(CircleArt, {covers, alt: ''}), createElement(CircleCard, props), createElement(CircleCard, {...props, variant: 'person', initials: 'SA', coverTransformOrigin: '52% 62%'})]) {
    assert.doesNotMatch(renderToStaticMarkup(element), /--circle-cover-origin|--circle-cover-zoom/);
  }
  runtime.dispose();
});

test('explicit ProfileFace palettes preserve supplied semantic/tint and identity inputs while selecting only the two-stop presentation', () => {
  const palette = Object.freeze({first: '#3f8f6e', second: '#153749'});
  const html = renderToStaticMarkup(createElement(ProfileFace, {initial: 'S', alt: 'Supplied viewer', size: 22, art: 'gold', tint: '#123456', palette, locked: true, selected: true}));
  assert.match(html, /profileFace profilePalette currentFace/);
  assert.match(html, /--profile-colour:#123456;--profile-first:#3f8f6e;--profile-second:#153749/);
  assert.match(html, /aria-label="Supplied viewer"/);
  assert.match(html, />S<span class="lockBadge"/);
  for (const props of [{}, {tint: '#3f8f6e'}]) {
    const standard = renderToStaticMarkup(createElement(ProfileFace, {initial: 'S', ...props}));
    assert.doesNotMatch(standard, /profilePalette|--profile-first|--profile-second/);
  }
  assert.deepEqual(palette, {first: '#3f8f6e', second: '#153749'});
});


test('CoverCard reserves generated copy only while the card selection overlay is visible', async () => {
  const {CoverCard} = await import('../../src/kit/cards/MediaCards.tsx');
  const {GeneratedCover} = await import('../../src/kit/artwork/GeneratedArt.tsx');
  const generated = {title: 'Supplied title', glyph: 'S', tint: {first: '#3f8f6e', second: '#153749'}};
  const model = {title: generated.title, caption: []};
  for (const selection of [undefined, {active: false, checked: false, onToggle() {}, onExit() {}}, {active: true, checked: false, onToggle() {}, onExit() {}}, {active: true, checked: true, onToggle() {}, onExit() {}}, {active: false, checked: true, onToggle() {}, onExit() {}}]) {
    const html = renderToStaticMarkup(createElement(CoverCard, {model, generated, selection, onNavigate() {}}));
    assert.equal(html.includes('generatedCover selectionClearance'), !!(selection?.active || selection?.checked));
    assert.match(html, /aria-labelledby="([^"]+)"[^>]*>[\s\S]*?<span id="\1" class="accessible">Supplied title<\/span>/);
  }
  const standalone = renderToStaticMarkup(createElement(GeneratedCover, generated));
  assert.doesNotMatch(standalone, /selectionClearance/);
});
