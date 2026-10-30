import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeModules, Platform} from './react-native-stub.mjs';
import {selectionHaptic, touchMenuHaptic} from '../src-next/platform/haptics.ts';
const modules = NativeModules as unknown as Record<string, unknown>;

test('selection entry makes one real request and returns only native admission', async () => {
  const prior = modules.PorticoHaptics, os = Platform.OS, tv = Platform.isTV;
  let calls = 0, admitted = true;
  modules.PorticoHaptics = {selection: async () => { calls++; return admitted; }};
  try {
    Platform.isTV = false;
    for (const target of ['android', 'ios']) {
      Platform.OS = target;
      assert.equal(await selectionHaptic(), true);
      admitted = false;
      assert.equal(await selectionHaptic(), false);
      admitted = true;
    }
    assert.equal(calls, 4);
    assert.equal(await selectionHaptic(false), false);
    assert.equal(calls, 4);
  } finally { modules.PorticoHaptics = prior; Platform.OS = os; Platform.isTV = tv; }
});

test('absent, rejected, malformed and unsupported haptic capabilities never report success', async () => {
  const prior = modules.PorticoHaptics, os = Platform.OS, tv = Platform.isTV;
  try {
    Platform.OS = 'android'; Platform.isTV = false;
    delete modules.PorticoHaptics;
    assert.equal(await selectionHaptic(), false);
    modules.PorticoHaptics = {selection: async () => { throw new Error('Unavailable'); }};
    assert.equal(await selectionHaptic(), false);
    modules.PorticoHaptics = {selection: async () => 'true'};
    assert.equal(await selectionHaptic(), false);
    modules.PorticoHaptics = {selection: () => { throw new Error('Must not be called'); }};
    Platform.isTV = true; assert.equal(await selectionHaptic(), false);
    Platform.OS = 'ios'; assert.equal(await selectionHaptic(), false);
    Platform.isTV = false; Platform.OS = 'web'; assert.equal(await selectionHaptic(), false);
  } finally { modules.PorticoHaptics = prior; Platform.OS = os; Platform.isTV = tv; }
});

test('touch menus request the firm platform capability once without changing selection feedback', async () => {
  const prior = modules.PorticoHaptics, os = Platform.OS, tv = Platform.isTV;
  const calls: string[] = [];
  modules.PorticoHaptics = {
    selection: async () => {calls.push('selection'); return true;},
    contextMenu: async () => {calls.push('contextMenu'); return true;},
  };
  try {
    Platform.isTV = false;
    Platform.OS = 'ios';
    assert.equal(await touchMenuHaptic(), true);
    assert.deepEqual(calls, ['contextMenu']);
    assert.equal(await selectionHaptic(), true);
    Platform.OS = 'android';
    assert.equal(await touchMenuHaptic(), true);
    assert.deepEqual(calls, ['contextMenu', 'selection', 'selection']);
    assert.equal(await touchMenuHaptic(false), false);
    assert.equal(calls.length, 3);
  } finally {modules.PorticoHaptics = prior; Platform.OS = os; Platform.isTV = tv;}
});

test('touch menu admission is strict and an absent Apple menu bridge never falls back to selection', async () => {
  const prior = modules.PorticoHaptics, os = Platform.OS, tv = Platform.isTV;
  try {
    Platform.OS = 'ios'; Platform.isTV = false;
    modules.PorticoHaptics = {selection: () => {throw new Error('Must not be called');}};
    assert.equal(await touchMenuHaptic(), false);
    for (const value of [false, 'true', undefined]) {
      modules.PorticoHaptics = {contextMenu: async () => value};
      assert.equal(await touchMenuHaptic(), false);
    }
    modules.PorticoHaptics = {contextMenu: async () => {throw new Error('Unavailable');}};
    assert.equal(await touchMenuHaptic(), false);
    modules.PorticoHaptics = {
      selection: () => {throw new Error('Must not be called');},
      contextMenu: () => {throw new Error('Must not be called');},
    };
    Platform.isTV = true;
    for (const target of ['ios', 'android']) {
      Platform.OS = target;
      assert.equal(await touchMenuHaptic(), false);
    }
    Platform.isTV = false; Platform.OS = 'web';
    assert.equal(await touchMenuHaptic(), false);
  } finally {modules.PorticoHaptics = prior; Platform.OS = os; Platform.isTV = tv;}
});

test('native source uses policy-respecting view feedback and UIKit without permissions or simulated receipts', () => {
  const android = readFileSync(new URL('../android/app/src/main/java/tv/getportico/portico/native/PorticoHaptics.kt', import.meta.url), 'utf8');
  assert.match(android, /performHapticFeedback\(HapticFeedbackConstants\.LONG_PRESS\)/);
  assert.match(android, /promise\.resolve\(admitted\)/);
  assert.doesNotMatch(android, /IGNORE_GLOBAL_SETTING|IGNORE_VIEW_SETTING|VIBRATE|VibrationEffect/);
  const apple = readFileSync(new URL('../Native/PorticoHaptics.m', import.meta.url), 'utf8');
  assert.match(apple, /TARGET_OS_TV \|\| TARGET_OS_SIMULATOR/);
  assert.match(apple, /\[feedback selectionChanged\]/);
  for (const platform of ['ios', 'tvos']) {
    const project = readFileSync(new URL(`../${platform}/Portico.xcodeproj/project.pbxproj`, import.meta.url), 'utf8');
    assert.match(project, /PorticoHaptics\.m in Sources/);
  }
});
