import test from 'node:test';
import assert from 'node:assert/strict';
import {playOnDestinations, webDestinationSupport} from '../src/presentation/index.ts';
import {enUS} from '../../i18n/src/index.ts';
import {isIconId} from '../../design/src/index.ts';

test('iPhone: AirPlay, Google Cast, then Portico devices on this network; no code entry (X-18)', () => {
  const d = playOnDestinations('ios', {airplay: true, googleCast: true, nearby: [{id: 't1', name: 'Living room', kind: 'tv'}, {id: 'p1', name: 'Sam’s iPad', kind: 'tablet'}]});
  assert.equal(d.offered, true);
  assert.deepEqual(d.rows.map(r => r.kind), ['airplay', 'googleCast', 'porticoDevice', 'porticoDevice']);
  assert.equal(d.nearbyHeading, 'playOn.onThisNetwork');
  assert.equal(d.empty, undefined);
  assert.deepEqual(d.rows.filter(r => r.kind === 'porticoDevice').map(r => r.icon), ['tvDevice', 'tablet']);
  for (const r of d.rows) {
    assert.ok(isIconId(r.icon));
    for (const id of ['label' in r ? r.label : undefined, 'subtitle' in r ? r.subtitle : undefined].filter(Boolean)) assert.ok((id as string) in enUS);
  }
  assert.ok(!('playOn.enterCode' in enUS), 'code entry is gone');
});

test('nothing found says so; televisions offer nothing', () => {
  const none = playOnDestinations('web');
  assert.deepEqual(none.rows, []);
  assert.equal(none.empty, 'playOn.noneNearby');
  assert.deepEqual(playOnDestinations('tvos', {airplay: true, googleCast: true, nearby: [{id: 'x', name: 'x', kind: 'tv'}]}), {offered: false, rows: []});
  assert.equal(playOnDestinations('androidtv').offered, false);
  assert.equal(playOnDestinations('firetv').offered, false);
  assert.equal(playOnDestinations('vega').offered, false);
});

test('web: Cast only on Chromium with the SDK, AirPlay only in Safari', () => {
  assert.deepEqual(webDestinationSupport({chromium: true, castApi: true}), {googleCast: true, airplay: false});
  assert.deepEqual(webDestinationSupport({safariAirPlay: true}), {googleCast: false, airplay: true});
  assert.deepEqual(playOnDestinations('web', webDestinationSupport({chromium: true, castApi: true})).rows.map(r => r.kind), ['googleCast']);
});
