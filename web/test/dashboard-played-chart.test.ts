import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {Chart} from '../src/screens/dashboard/Chart.tsx';

// Play history draws on the shared chart (one per platform): stacked, each kind on the ones before it.
test('a stacked chart draws each kind as its own band over the kinds before it', () => {
  const days = [0, 1, 2].map(day => Date.UTC(2026, 10, 10 + day));
  const interval = {period: '7d', timeZone: 'UTC', from: new Date(days[0]!).toISOString(), until: new Date(days[2]!).toISOString(), buckets: days.map(start => ({start: new Date(start).toISOString(), end: new Date(start + 86_400_000).toISOString(), partial: false}))};
  const series = [{key: 'movies', label: 'Movies', points: days.map(t => ({t, v: 1}))}, {key: 'tv', label: 'TV', points: days.map(t => ({t, v: 2}))}];
  const html = renderToStaticMarkup(createElement(Chart, {stacked: true, kind: 'played', interval, title: 'Time played', series, format: (v: number) => `${v} h`}));
  const bands = [...html.matchAll(/<g data-series="(\w+)"><path class="area" d="M[\d.]+,([\d.]+)/g)].map(m => [m[1], Number(m[2])]);
  assert.deepEqual(bands.map(b => b[0]), ['movies', 'tv']);
  assert.ok((bands[1]![1] as number) < (bands[0]![1] as number), 'TV is drawn on top of Movies, higher up');
  assert.match(html, /Total.*Movies 3 h.*TV 6 h/s, 'the legend gives each kind\'s total, not its average');
});
