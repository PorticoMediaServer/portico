import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import type {CastPlayerProps} from '../src/kit/player/types.ts';

function stubLoader() {
  return registerHooks({load(url, ctx, next) {
    if (url.endsWith('/kit/overlays/index.ts')) return {format: 'module', source: "export function Dialog({open, children}) { return open ? children : null; }", shortCircuit: true};
    if (url.endsWith('/kit/Icon.tsx')) return {format: 'module', source: "export function Icon() { return null; }", shortCircuit: true};
    if (url.endsWith('/kit/artwork/index.ts')) return {format: 'module', source: "export const AmbientArt = () => null, Artwork = () => null, useCoverAspect = () => ({aspect: undefined, onReady: () => {}}), wholeCoverBox = () => undefined;", shortCircuit: true};
    if (url.endsWith('/kit/controls/index.ts')) return {format: 'module', source: "export const Button = () => null, Slider = () => null;", shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, ctx);
  }});
}

function makeProps(overrides?: {
  cast?: Record<string, unknown>;
  remote?: Record<string, unknown>;
  onAudio?: (remote: unknown, id: unknown) => void;
  onSubtitles?: (remote: unknown, id: unknown) => void;
}): CastPlayerProps {
  return {
    controller: {
      closePanel() {},
      audio(remote: unknown, id: unknown) { overrides?.onAudio?.(remote, id); },
      subtitles(remote: unknown, id: unknown) { overrides?.onSubtitles?.(remote, id); },
    } as unknown as CastPlayerProps['controller'],
    reportError() {},
    phone: false,
    t: id => id,
    visuals: {skipBackSeconds: 10, skipForwardSeconds: 30},
    snapshot: {
      disposed: false,
      panel: 'tracks',
      airPlayAvailable: false,
      cast: {
        available: true,
        phase: 'casting',
        positionSeconds: 10,
        durationSeconds: 120,
        paused: false,
        audioTracks: [{id: 1, language: 'en', name: 'English'}, {id: 2, language: 'fr', name: 'French'}],
        textTracks: [{id: 10, language: 'en', name: 'English CC'}, {id: 11, language: 'es', name: 'Spanish'}],
        ...overrides?.cast,
      },
      remote: {
        id: 'cast-remote-1',
        sequence: 1,
        phase: 'playing',
        confirmed: true,
        itemId: 'item-1',
        title: 'Film',
        deviceName: 'Living Room TV',
        positionKnown: true,
        positionSeconds: 10,
        durationSeconds: 120,
        paused: false,
        ...overrides?.remote,
      },
    } as unknown as CastPlayerProps['snapshot'],
  };
}

test('CastTracks marks active Cast audio and subtitle tracks from active ids and track flags', async t => {
  const loader = stubLoader();
  t.after(() => loader.deregister());
  const {CastTracks} = await import('../src/kit/player/CastPlayer.tsx');

  const fromIds = renderToStaticMarkup(createElement(CastTracks, makeProps({
    cast: {activeAudioTrackId: 2, activeTextTrackId: 11},
  })));
  assert.match(fromIds, /role="radio" aria-checked="true"[^>]*aria-label="French"/);
  assert.match(fromIds, /role="radio" aria-checked="false"[^>]*aria-label="English"/);
  assert.match(fromIds, /role="radio" aria-checked="true"[^>]*aria-label="Spanish"/);
  assert.match(fromIds, /role="radio" aria-checked="false"[^>]*aria-label="next\.controls\.off"/);

  const subsOff = renderToStaticMarkup(createElement(CastTracks, makeProps({
    cast: {activeAudioTrackId: 1, activeTextTrackId: null},
  })));
  assert.match(subsOff, /role="radio" aria-checked="true"[^>]*aria-label="next\.controls\.off"/);
  assert.match(subsOff, /role="radio" aria-checked="false"[^>]*aria-label="English CC"/);
  assert.match(subsOff, /role="radio" aria-checked="true"[^>]*aria-label="English"/);

  const fromFlags = renderToStaticMarkup(createElement(CastTracks, makeProps({
    cast: {
      audioTracks: [{id: 1, language: 'en', name: 'English', active: true}, {id: 2, language: 'fr', name: 'French'}],
      textTracks: [{id: 10, language: 'en', name: 'English CC', active: false}, {id: 11, language: 'es', name: 'Spanish', active: false}],
    },
  })));
  assert.match(fromFlags, /role="radio" aria-checked="true"[^>]*aria-label="English"/);
  assert.match(fromFlags, /role="radio" aria-checked="true"[^>]*aria-label="next\.controls\.off"/);
});

test('CastTracks updates active indicators immediately on user track switch and resets across items', async t => {
  // Exercise CastTracks's state transitions via a minimal stateful hook harness without DOM overhead.
  const loader = stubLoader();
  t.after(() => loader.deregister());

  const React = await import('react');
  const {CastTracks} = await import('../src/kit/player/CastPlayer.tsx');
  const internals = (React as unknown as {__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE: {H: unknown}}).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;
  let state: unknown = null;
  const audioCalls: unknown[] = [], subtitleCalls: unknown[] = [];

  const render = (props: CastPlayerProps) => {
    const prev = internals.H;
    internals.H = {
      useState(initial: unknown) {
        if (state === null) state = typeof initial === 'function' ? (initial as () => unknown)() : initial;
        return [state, (next: unknown) => { state = typeof next === 'function' ? (next as (s: unknown) => unknown)(state) : next; }];
      },
    };
    try {
      const dialog = CastTracks(props) as unknown as {props: {children: Array<{props: {title: string; choices: Array<{id: string; selected: boolean}>; onSelect: (id: string) => void}}>}};
      return dialog.props.children.filter(Boolean).map(child => child.props);
    } finally {
      internals.H = prev;
    }
  };

  const baseProps = makeProps({
    onAudio: (_r, id) => audioCalls.push(id),
    onSubtitles: (_r, id) => subtitleCalls.push(id),
  });

  // Switch audio to track 2 and subtitles to track 11
  let sections = render(baseProps);
  const audioSection = sections.find(s => s.title === 'next.cast.audio')!;
  const subSection = sections.find(s => s.title === 'next.cast.subtitles')!;
  audioSection.onSelect('2');
  subSection.onSelect('11');
  assert.deepEqual(audioCalls, [2]);
  assert.deepEqual(subtitleCalls, [11]);

  sections = render(baseProps);
  assert.equal(sections.find(s => s.title === 'next.cast.audio')!.choices.find(c => c.id === '2')!.selected, true);
  assert.equal(sections.find(s => s.title === 'next.cast.audio')!.choices.find(c => c.id === '1')!.selected, false);
  assert.equal(sections.find(s => s.title === 'next.cast.subtitles')!.choices.find(c => c.id === '11')!.selected, true);
  assert.equal(sections.find(s => s.title === 'next.cast.subtitles')!.choices.find(c => c.id === 'off')!.selected, false);

  // Switch subtitles to off
  sections.find(s => s.title === 'next.cast.subtitles')!.onSelect('off');
  assert.deepEqual(subtitleCalls, [11, null]);
  sections = render(baseProps);
  assert.equal(sections.find(s => s.title === 'next.cast.subtitles')!.choices.find(c => c.id === 'off')!.selected, true);
  assert.equal(sections.find(s => s.title === 'next.cast.subtitles')!.choices.find(c => c.id === '11')!.selected, false);

  // When receiver subsequently reports a different active audio track, receiver truth wins
  sections = render(makeProps({cast: {activeAudioTrackId: 1}}));
  assert.equal(sections.find(s => s.title === 'next.cast.audio')!.choices.find(c => c.id === '1')!.selected, true);
  assert.equal(sections.find(s => s.title === 'next.cast.audio')!.choices.find(c => c.id === '2')!.selected, false);

  // Queue advance to a new item resets stale local selections
  sections = render(makeProps({remote: {itemId: 'item-2'}}));
  assert.equal(sections.find(s => s.title === 'next.cast.audio')!.choices.every(c => !c.selected), true);
  assert.equal(sections.find(s => s.title === 'next.cast.subtitles')!.choices.every(c => !c.selected), true);
});
