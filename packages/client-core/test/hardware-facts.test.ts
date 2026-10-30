import test from 'node:test';
import assert from 'node:assert/strict';
import {parseHardwareInventory, readHardwareInventory} from '../src/hardware.ts';
import {parseTranscodeCapacity} from '../src/telemetry.ts';

const capacity = (hardware: Record<string, unknown>) => ({
  enabled: true, directStreamRemux: false, x264Preset: 'veryfast', planningPolicy: 'maximum_fidelity',
  throttleBufferMs: 0, playedRetentionMs: 0, observedAt: 0,
  hardware: {configured: 'auto', effective: 'vaapi', device: '', probes: [], lastTest: 0, ...hardware},
  sessions: {active: null, hardware: null, software: null, background: null, limits: {concurrent: 0, hardware: 0, software: 0, background: 0}},
  hdrToneMapping: {status: 'available', detail: 'on the vaapi backend'},
  temporaryDirectory: {path: '/tmp', ready: true, freeBytes: null}, presets: [], dependencies: {}, warnings: [],
});

test('the measured hardware half reads as published, and a row it cannot read is left out', () => {
  const parsed = parseTranscodeCapacity(capacity({
    devices: [{id: 'card-0', name: 'Intel UHD Graphics 770', method: 'quick_sync', inUse: true}, {id: '', name: 'broken'}],
    formats: [{format: 'h264', decode: true, encode: false}, {format: 'av1'}],
    lastError: {reason: 'no_graphics_device'}, lastTest: 1_700_000_000,
  }));
  assert.deepEqual(parsed.hardware.devices, [{id: 'card-0', name: 'Intel UHD Graphics 770', method: 'quick_sync', inUse: true}]);
  assert.deepEqual(parsed.hardware.formats, [{format: 'h264', decode: true, encode: false}]);
  assert.equal(parsed.hardware.lastError?.reason, 'no_graphics_device');
  assert.equal(parsed.hardware.lastTest, 1_700_000_000);
});

test('an older server without the measured hardware reads as no devices, not as an error', () => {
  const parsed = parseTranscodeCapacity(capacity({}));
  assert.deepEqual(parsed.hardware.devices, []);
  assert.deepEqual(parsed.hardware.formats, []);
  assert.equal(parsed.hardware.lastError, undefined);
  assert.equal(parsed.hardware.lastTest, 0);
});

test('an unreadable fault is dropped while its device and format rows stay', () => {
  const parsed = parseTranscodeCapacity(capacity({
    devices: [{id: 'card-1', name: 'NVIDIA GeForce RTX 4070', method: 'nvenc', inUse: false}],
    formats: [{format: 'hevc10', decode: true, encode: true}],
    lastError: {detail: 'no reason code'},
  }));
  assert.equal(parsed.hardware.lastError, undefined);
  assert.equal(parsed.hardware.devices.length, 1);
  assert.deepEqual(parsed.hardware.formats, [{format: 'hevc10', decode: true, encode: true}]);
});

test('the install remedy is the server’s own wording, and unknown rows are left out', () => {
  const inventory = parseHardwareInventory({platform: {kind: 'docker_intel_amd', label: 'Docker', container: true,
    remedy: 'Give Portico your graphics card: add the device /dev/dri to this container.'},
  targets: [{id: 'intel', label: 'Intel (Linux, Windows)', decode: ['H.264'], encode: ['H.264'], toneMapping: ['VPP'], notes: []}],
  formats: [{id: 'h264', label: 'H.264'}, {id: '', label: 'broken'}],
  later: [{id: 'vulkan_video', label: 'Vulkan Video encoding', detail: 'After V1.'}]});
  assert.equal(inventory.platform.remedy, 'Give Portico your graphics card: add the device /dev/dri to this container.');
  assert.deepEqual(inventory.formats, [{id: 'h264', label: 'H.264'}]);
  assert.equal(inventory.targets[0]?.encode[0], 'H.264');
  assert.equal(inventory.later[0]?.id, 'vulkan_video');
});

test('the installer’s platform without a remedy does not invent one', async () => {
  const api = {request: async () => ({platform: {kind: 'macos', label: 'macOS', container: false}, targets: [], formats: [], later: []})};
  const inventory = await readHardwareInventory(api);
  assert.equal(inventory.platform.remedy, undefined);
});
