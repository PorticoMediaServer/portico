import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';

// Under an upright live picture the watch page is the channel's (owner, 18 Nov; phone-player 1c and 1e): its mark, name,
// number and Live, what is on with its times, Guide, and Record only where the channel can record.
const {LivePage} = await import('../src/kit/player/LivePage.tsx');
const channel = {id: 'chn_1', sourceId: 'src', kind: 'live-source', generation: 'g', number: '101', name: 'Northwind News', mark: {text: 'NN', seed: 'src:chn_1'}, favorite: false, recordAvailable: true, tuneAvailable: true};
const port = (over: Record<string, unknown> = {}) => ({channel, programme: {id: 'pg', title: 'World Tonight', startMs: Date.parse('2026-11-21T20:30:00Z'), endMs: Date.parse('2026-11-21T21:30:00Z')},
  nextProgramme: {title: 'Morning Briefing', startMs: Date.parse('2026-11-21T21:30:00Z'), endMs: Date.parse('2026-11-21T22:30:00Z')},
  isCurrent: () => true, record: async () => {}, openFullGuide: () => {}, ...over}) as never;

test('the live page names the channel and what is on, with Guide and Record', () => {
  const html = renderToStaticMarkup(createElement(LivePage, {port: port(), reportError() {}}));
  for (const text of ['Northwind News', '101', 'World Tonight', 'Morning Briefing', 'Guide', 'Record']) assert.ok(html.includes(text), text);
  assert.match(html, /<h1[^>]*>World Tonight<\/h1>/);
});

test('a channel that cannot record offers no Record', () => {
  const html = renderToStaticMarkup(createElement(LivePage, {port: port({channel: {...channel, recordAvailable: false}}), reportError() {}}));
  assert.ok(!html.includes('>Record<'));
  assert.ok(html.includes('Guide'));
});
