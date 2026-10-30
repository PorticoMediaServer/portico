import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules, Platform} from './react-native-stub.mjs';
import {ClipboardUnavailableError, copyText} from '../src-next/platform/clipboard.ts';

const modules = NativeModules as unknown as Record<string, unknown>;
const unavailable = (error: unknown) => error instanceof ClipboardUnavailableError && error.code === 'clipboard_unavailable';

test('Android and iOS forward the exact text to the installed native clipboard without reading it', async () => {
  const previous = modules.Clipboard;
  const oldOS = Platform.OS;
  const oldTV = Platform.isTV;
  const calls: string[] = [];
  modules.Clipboard = {setString: (text: string) => calls.push(text), getString: () => {throw new Error('Clipboard read is forbidden');}};
  try {
    Platform.isTV = false;
    for (const os of ['android', 'ios']) {
      Platform.OS = os;
      await copyText('Diagnostic reference\nRésumé — α');
      await copyText('');
    }
    Platform.OS = 'android'; Platform.isTV = true;
    await copyText('TV diagnostic reference');
    assert.deepEqual(calls, ['Diagnostic reference\nRésumé — α', '', 'Diagnostic reference\nRésumé — α', '', 'TV diagnostic reference']);
  } finally {Platform.OS = oldOS; Platform.isTV = oldTV; if (previous === undefined) delete modules.Clipboard; else modules.Clipboard = previous;}
});

test('an absent native clipboard or setter fails explicitly instead of reporting a successful copy', async () => {
  const previous = modules.Clipboard;
  const oldOS = Platform.OS;
  Platform.OS = 'android';
  try {
    delete modules.Clipboard;
    await assert.rejects(copyText('Details'), unavailable);
    modules.Clipboard = {};
    await assert.rejects(copyText('Details'), unavailable);
  } finally {Platform.OS = oldOS; if (previous === undefined) delete modules.Clipboard; else modules.Clipboard = previous;}
});

test('tvOS and unsupported operating systems reject without invoking the native no-op', async () => {
  const previous = modules.Clipboard;
  const oldOS = Platform.OS;
  const oldTV = Platform.isTV;
  let writes = 0;
  modules.Clipboard = {setString: () => {writes++;}};
  try {
    Platform.OS = 'ios'; Platform.isTV = true;
    await assert.rejects(copyText('Details'), unavailable);
    Platform.OS = 'web'; Platform.isTV = false;
    await assert.rejects(copyText('Details'), unavailable);
    assert.equal(writes, 0);
  } finally {Platform.OS = oldOS; Platform.isTV = oldTV; if (previous === undefined) delete modules.Clipboard; else modules.Clipboard = previous;}
});

test('native synchronous and asynchronous write failures propagate; success waits for the write', async () => {
  const previous = modules.Clipboard;
  const oldOS = Platform.OS;
  Platform.OS = 'android';
  const failure = new Error('Native write failed');
  try {
    modules.Clipboard = {setString: () => {throw failure;}};
    await assert.rejects(copyText('Details'), error => error === failure);
    modules.Clipboard = {setString: async () => {throw failure;}};
    await assert.rejects(copyText('Details'), error => error === failure);
    let finish!: () => void;
    let complete = false;
    modules.Clipboard = {setString: () => new Promise<void>(resolve => {finish = resolve;})};
    const pending = copyText('Details').then(() => {complete = true;});
    await Promise.resolve();
    assert.equal(complete, false);
    finish(); await pending;
    assert.equal(complete, true);
  } finally {Platform.OS = oldOS; if (previous === undefined) delete modules.Clipboard; else modules.Clipboard = previous;}
});
