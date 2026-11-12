import test from 'node:test';
import assert from 'node:assert/strict';
import {bookRecording, endRecording, markChannel, readGuide, readRecordings, removeRule, saveRule, setReminder, upcomingOrder} from '../src/live-tv.ts';

type Sent = {path: string; method: string; body: unknown};
const server = () => {
  const sent: Sent[] = [];
  return {sent, api: {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => { sent.push({path, method, body}); return {} as T; }}};
};

test('the guide asks for a window and the chosen channels, and leaves the server its defaults when none is given', async () => {
  const {sent, api} = server();
  await readGuide(api);
  await readGuide(api, {from: new Date('2026-11-12T18:00:00Z'), to: new Date('2026-11-12T21:00:00Z')}, {favorites: true, sort: 'name'}, 'c2');
  assert.deepEqual(sent.map(one => one.path), ['/v1/live/guide',
    '/v1/live/guide?favorites=true&sort=name&from=2026-11-12T18%3A00%3A00.000Z&to=2026-11-12T21%3A00%3A00.000Z&cursor=c2']);
});

test('a channel is favourited and hidden for this profile, and a reminder is set and taken off by its programme', async () => {
  const {sent, api} = server();
  await markChannel(api, 'ch_1', {favorite: true, hidden: false});
  await setReminder(api, 'pg_1', true);
  await setReminder(api, 'pg_1', false);
  assert.deepEqual(sent.map(one => [one.method, one.path, one.body]), [['PUT', '/v1/live/channels/ch_1/state', {favorite: true, hidden: false}],
    ['PUT', '/v1/live/reminders/pg_1', undefined], ['DELETE', '/v1/live/reminders/pg_1', undefined]]);
});

test('recordings: History and everyone\'s are asked for by name, a booking is an airing, stopping keeps what was captured, a rule goes with its bookings kept or cancelled', async () => {
  const {sent, api} = server();
  await readRecordings(api, {history: true, everyone: true});
  await bookRecording(api, {programme: 'pg_1', padAfterMs: 120_000});
  await endRecording(api, 'rec_1', 'stop');
  await endRecording(api, 'rec_2', 'cancel');
  await saveRule(api, {programme: 'pg_1', newOnly: true}, true);
  await removeRule(api, 'rule_1', 'keep');
  assert.deepEqual(sent.map(one => [one.method, one.path]), [['GET', '/v1/dvr/recordings?state=history&scope=everyone'], ['POST', '/v1/dvr/recordings'],
    ['DELETE', '/v1/dvr/recordings/rec_1?action=stop'], ['DELETE', '/v1/dvr/recordings/rec_2'], ['POST', '/v1/dvr/rules?preview=true'], ['DELETE', '/v1/dvr/rules/rule_1?bookings=keep']]);
});

test('upcoming bookings show conflicts first, then by start', () => {
  const at = (id: string, startsAt: string, conflict = false) => ({id, startsAt, conflict}) as never;
  assert.deepEqual(upcomingOrder([at('a', '2026-11-12T20:00:00Z'), at('b', '2026-11-12T22:00:00Z', true), at('c', '2026-11-12T19:00:00Z')]).map((one: {id: string}) => one.id), ['b', 'c', 'a']);
});
