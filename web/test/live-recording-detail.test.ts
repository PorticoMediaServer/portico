import test from 'node:test';
import assert from 'node:assert/strict';
import type {ScreenContext} from '../src/screens/detail/context.ts';
import {readRecordingDetail} from '../src/screens/live/recording-detail.ts';

test('readRecordingDetail forwards AbortSignal into fetches and disposes screenLifetime on completion and abort', async () => {
  const seenSignals: (AbortSignal | undefined)[] = [];
  const context = {
    isCurrent: () => true,
    api: {
      async request(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal) {
        seenSignals.push(signal);
        return {
          id: 'rec_1',
          channel: 'ch_1',
          title: 'Evening News',
          startsAt: '2026-11-12T20:00:00Z',
          endsAt: '2026-11-12T21:00:00Z',
          padBeforeMs: 0,
          padAfterMs: 0,
          priority: 0,
          state: 'scheduled',
          inGuide: true,
          keep: false,
          fromRule: false,
          tuners: 2,
        };
      },
    },
  } as unknown as ScreenContext;

  const controller = new AbortController();
  const detail = await readRecordingDetail(context, 'rec_1', controller.signal);
  assert.equal(detail.recording.id, 'rec_1');
  assert.equal(detail.capacity.effective, 2);
  assert.equal(seenSignals.length, 1);
  // screenLifetime must have been disposed in finally, aborting the request signal.
  assert.equal(seenSignals[0]?.aborted, true, 'screenLifetime must be disposed after detail resolves');

  // Pre-aborted signal must not start any fetch.
  const aborted = new AbortController();
  aborted.abort();
  await assert.rejects(readRecordingDetail(context, 'rec_1', aborted.signal), {name: 'AbortError'});
  assert.equal(seenSignals.length, 1, 'aborted caller signal must not issue any HTTP request');

  // Mid-flight abort must immediately abort the signal forwarded into context.api.request.
  let inFlightSignal: AbortSignal | undefined;
  const hangingContext = {
    isCurrent: () => true,
    api: {
      request(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal) {
        inFlightSignal = signal;
        return new Promise((_resolve, reject) => {
          signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {once: true});
        });
      },
    },
  } as unknown as ScreenContext;
  const midFlight = new AbortController();
  const pending = readRecordingDetail(hangingContext, 'rec_2', midFlight.signal);
  assert.ok(inFlightSignal);
  assert.equal(inFlightSignal.aborted, false);
  midFlight.abort();
  assert.equal(inFlightSignal.aborted, true, 'aborting caller signal must immediately abort forwarded fetch signal');
  await assert.rejects(pending, {name: 'AbortError'});
});
