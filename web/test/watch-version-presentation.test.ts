import test from 'node:test';
import assert from 'node:assert/strict';
import type {PlaybackOptions, MediaVersion} from '@core/playback-v1/types.ts';
import type {ShellAssemblyContext} from '../src/shell/bootstrap.ts';
import {createWatchVersionPresentation, openWatchVersionChooser, registerWatchVersionPresentation, type WatchVersionRequest} from '../src/screens/watch/version-presentation.ts';

const version = (id: string): MediaVersion => ({id, label: 'not a chooser label', durationMs: 1000, parts: [], video: [], audio: [], subtitles: []});
function request(change: Partial<WatchVersionRequest> = {}): WatchVersionRequest {
  const options: PlaybackOptions = {itemId: 'actual_item', kind: 'movie', versions: [version('first'), version('second')], chapters: [], markers: [], qualities: []};
  return {subject: {libraryId: 'actual_library', itemId: options.itemId, title: 'Stored title'}, options,
    signal: new AbortController().signal, isCurrent: () => true, playVersion: async () => {}, ...change};
}

test('the title chooser retains original options and forwards only an offered exact version with its caller lifetime', async () => {
  const owner = createWatchVersionPresentation(() => true), source = new AbortController(), action = new AbortController();
  let received: {id: string; signal: AbortSignal} | undefined;
  const value = request({signal: source.signal, playVersion: async (id, signal) => {received = {id, signal};}});
  const done = owner.open(value), shown = owner.getSnapshot()!;
  assert.equal(shown.request, value);assert.equal(shown.choices.options, value.options);assert.equal(shown.choices.versions, value.options.versions);
  assert.equal(shown.choices.selectedVersionId, undefined, 'a title menu has no fabricated playing selection');
  await shown.playVersion('second', action.signal);assert.equal(received!.id, 'second');assert.equal(received!.signal.aborted, false);
  await assert.rejects(shown.playVersion('foreign', action.signal));
  source.abort();await done;assert.equal(received!.signal.aborted, true);assert.equal(owner.getSnapshot(), undefined);assert.equal(shown.choices.isCurrent(), false);
  owner.dispose();
});

test('explicit close and replacement abort only their own modal action, while a failed send retains the same chooser', async () => {
  const owner = createWatchVersionPresentation(() => true);
  const original = request({playVersion: async () => {throw new Error('actual start failure');}}), firstDone = owner.open(original), first = owner.getSnapshot()!;
  await assert.rejects(first.playVersion('first', new AbortController().signal), /actual start failure/);
  assert.equal(owner.getSnapshot(), first);assert.equal(first.choices.isCurrent(), true);
  const nextDone = owner.open(request()), next = owner.getSnapshot()!;await firstDone;
  assert.equal(first.choices.isCurrent(), false);assert.equal(next.choices.isCurrent(), true);
  await assert.rejects(first.playVersion('first', new AbortController().signal));
  owner.close();await nextDone;assert.equal(next.choices.isCurrent(), false);owner.dispose();
});

test('only the host registered for the genuine captured context can present a title chooser', async () => {
  let current = true;
  const context = {isCurrent: () => current} as ShellAssemblyContext, foreign = {isCurrent: () => true} as ShellAssemblyContext;
  const owner = createWatchVersionPresentation(context.isCurrent), release = registerWatchVersionPresentation(context, owner);
  await assert.rejects(openWatchVersionChooser(foreign, request()), /Watch authority retired/);
  assert.throws(() => registerWatchVersionPresentation(context, owner), /already_registered/);
  const done = openWatchVersionChooser(context, request());assert.ok(owner.getSnapshot());
  current = false;assert.equal(owner.getSnapshot()!.choices.isCurrent(), false);release();await done;
  await assert.rejects(openWatchVersionChooser(context, request()));
  const invalid = request();await assert.rejects(owner.open({...invalid, options: {...invalid.options, itemId: 'foreign'}}));
});
