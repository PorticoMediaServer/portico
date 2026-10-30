import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeObservationError, requestNativeObservations} from '../src-next/dev/layout-dump/native-observations.ts';
import {createNativeTreeObserver, createAndroidNativeTreeObserver} from '../src-next/dev/layout-dump/native-observer.ts';
import {collectNativeScreen} from '../src-next/dev/layout-dump/collect.ts';
import {createNativeMeasurementRegistry} from '../src-next/dev/layout-dump/registry.ts';

const native = (value: unknown) => ({value, source: 'android.observed.SDK'});
const response = (facts: Record<string, unknown> = {}) => ({version: 1, unit: 'dp', rootReactTag: 11, fixtureReactTag: 12,
  hosts: [{reactTag: 13, facts}]});
const request = (raw: unknown) => ({rootReactTag: 11, fixtureReactTag: 12, hostReactTags: [11, 12, 13],
  isCurrent: () => true, bridge: {observe: async () => raw}});
const failure = (code: string) => (error: unknown) => error instanceof NativeObservationError && error.code === code;

test('native observation keeps actual false/zero and unavailable separate without reading or forwarding other fields', async () => {
  let reads = 0;
  const facts = {isFocused: native(false), effectiveOpacity: native(0), textColor: native('rgba(10, 20, 30, 0.5)'),
    fontFamily: {unavailable: 'font-family-unavailable'},
    get props() {reads++; throw new Error('private field read');},
    get imageSource() {reads++; throw new Error('asset field read');}};
  const result = await requestNativeObservations(request(response(facts)));
  assert.deepEqual(result.hosts.map(host => host.reactTag), [11, 12, 13]);
  assert.deepEqual(result.hosts[0].facts.isFocused, {unavailable: 'host-not-found'});
  assert.deepEqual(result.hosts[2].facts.isFocused, native(false));
  assert.deepEqual(result.hosts[2].facts.effectiveOpacity, native(0));
  assert.deepEqual(result.hosts[2].facts.fontFamily, {unavailable: 'font-family-unavailable'});
  assert.deepEqual(result.hosts[2].facts.fontSize, {unavailable: 'native-fact-missing'});
  assert.equal(reads, 0);
  assert.equal('props' in result.hosts[2].facts, false);
  assert.equal('imageSource' in result.hosts[2].facts, false);
});

test('malformed native facts do not become paint, identity, focus or raw errors', async () => {
  let getterReads = 0;
  const result = await requestNativeObservations(request(response({
    isFocused: native('false'), effectiveOpacity: native(1.01), fontSize: native(NaN), fontWeight: native(700.5),
    textColor: native('rgba(999, 0, 0, 1)'), fontFamily: native('file:///private/font.ttf'),
    backgroundColor: {get value() {getterReads++; return 'rgba(0, 0, 0, 1)';}, source: 'android.SDK'},
    boxShadow: native('url(private-image)'), lineHeight: {unavailable: '/private/raw-error'},
    textLineRects: native([{x: 0, y: 0, width: -1, height: 30}]),
  })));
  for (const key of ['isFocused', 'effectiveOpacity', 'fontSize', 'fontWeight', 'textColor', 'fontFamily',
    'backgroundColor', 'boxShadow', 'lineHeight', 'textLineRects'] as const) {
    if (key === 'fontFamily') continue;
    assert.deepEqual(result.hosts[2].facts[key], {unavailable: 'invalid-native-fact'});
  }
  assert.equal(getterReads, 0);
  assert.deepEqual(result.hosts[2].facts.fontFamily, {unavailable: 'font-family-unavailable'});
});

test('optional native paint facts keep signed spacing, exact enums and explicit unsupported transforms', async () => {
  for (const letterSpacing of [-3.96, -0, 0, 1.5]) {
    const result = await requestNativeObservations(request(response({fontStyle: native('italic'), letterSpacing: native(letterSpacing),
      textDecorationLine: native('underline line-through'), textShadow: native('none'), textTransform: native('uppercase'),
      imageHasImage: native(false), imageScaleType: native('center-crop')})));
    const facts = result.hosts[2].facts;
    assert.deepEqual(facts.fontStyle, native('italic'));
    assert.ok('value' in facts.letterSpacing && Object.is(facts.letterSpacing.value, letterSpacing));
    assert.deepEqual(facts.textDecorationLine, native('underline line-through'));
    assert.deepEqual(facts.textShadow, native('none'));
    assert.deepEqual(facts.textTransform, {unavailable: 'paint-unprovable'});
    assert.deepEqual(facts.imageHasImage, native(false));
    assert.deepEqual(facts.imageScaleType, native('center-crop'));
  }
});

test('native mechanism facts never accept custom strings, active shadows, getters or image completion guesses', async () => {
  let reads = 0;
  const result = await requestNativeObservations(request(response({fontStyle: native('oblique'), letterSpacing: native(Infinity),
    textDecorationLine: native('line-through underline'), textShadow: native('0px 1px 2px rgba(0, 0, 0, 1)'),
    imageHasImage: native('true'), imageScaleType: native('custom(0.5)'),
    get imageLoaded() {reads++; throw new Error('unapproved image state was read');}})));
  for (const property of ['fontStyle', 'letterSpacing', 'textDecorationLine', 'textShadow', 'imageHasImage', 'imageScaleType'] as const) {
    assert.deepEqual(result.hosts[2].facts[property], {unavailable: 'invalid-native-fact'});
  }
  assert.equal(reads, 0);
  for (const reason of ['not-image', 'image-state-unavailable', 'unsupported-image-scale']) {
    const decoded = await requestNativeObservations(request(response({imageHasImage: {unavailable: reason}})));
    assert.deepEqual(decoded.hosts[2].facts.imageHasImage, {unavailable: reason});
  }
});

test('all agreed image scale and decoration mechanisms remain bounded internal facts', async () => {
  for (const imageScaleType of ['fit-xy', 'fit-x', 'fit-y', 'fit-start', 'fit-center', 'fit-end', 'center',
    'center-inside', 'center-crop', 'focus-crop', 'fit-bottom-start', 'matrix']) {
    const decoded = await requestNativeObservations(request(response({imageScaleType: native(imageScaleType)})));
    assert.deepEqual(decoded.hosts[2].facts.imageScaleType, native(imageScaleType));
  }
  for (const textDecorationLine of ['none', 'underline', 'line-through', 'underline line-through']) {
    const decoded = await requestNativeObservations(request(response({textDecorationLine: native(textDecorationLine)})));
    assert.deepEqual(decoded.hosts[2].facts.textDecorationLine, native(textDecorationLine));
  }
});

test('foreign roots, units, duplicate rows or unrequested hosts reject the whole observation', async () => {
  for (const raw of [{...response(), rootReactTag: 99}, {...response(), fixtureReactTag: 99}, {...response(), unit: 'px'},
    {...response(), hosts: [{reactTag: 99, facts: {}}]}, {...response(), hosts: [{reactTag: 13, facts: {}}, {reactTag: 13, facts: {}}]}]) {
    await assert.rejects(requestNativeObservations(request(raw)), failure('invalid-response'));
  }
});

test('native window evidence preserves negative origins and fractional extents without becoming a chosen box', async () => {
  const rect = {x: -4.5, y: 12.25, width: 390.125, height: 844.5};
  const result = await requestNativeObservations(request(response({windowRect: {value: rect,
    source: 'android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp'}})));
  assert.deepEqual(result.hosts[2].facts.windowRect, {value: rect,
    source: 'android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp'});
});

test('invalid IDs, duplicate IDs and retired fixture refuse before entering native', async () => {
  let calls = 0;
  const base = {...request(response()), bridge: {observe: async () => {calls++; return response();}}};
  for (const override of [{rootReactTag: 0}, {fixtureReactTag: 11}, {hostReactTags: [13, 13]},
    {hostReactTags: Array.from({length: 2001}, (_, i) => i + 1)}]) {
    await assert.rejects(requestNativeObservations({...base, ...override}), failure('invalid-request'));
  }
  await assert.rejects(requestNativeObservations({...base, isCurrent: () => false}), failure('scope-changed'));
  assert.equal(calls, 0);
});

test('native scope rejection discards the capture without reading or forwarding a native error message', async () => {
  let reads = 0;
  const bridge = {observe: async () => {throw {code: 'layout_observation_scope',
    get message() {reads++; throw new Error('private native detail');}};}};
  await assert.rejects(requestNativeObservations({...request(response()), bridge}), (error: unknown) =>
    failure('scope-changed')(error) && (error as NativeObservationError).boundary === 'native-scope-rejected-while-js-current');
  assert.equal(reads, 0);
});

test('scope failure boundaries preserve every currentness fence without exporting native details', async () => {
  for (const [failsAt, expected, nativeCalls] of [[1, 'js-before-native', 0], [2, 'js-before-native', 0],
    [3, 'js-after-native', 1], [4, 'js-after-decode', 1]] as const) {
    let checks = 0, calls = 0;
    await assert.rejects(requestNativeObservations({...request(response()), isCurrent: () => ++checks < failsAt,
      bridge: {observe: async () => {calls++; return response();}}}), (error: unknown) =>
      failure('scope-changed')(error) && (error as NativeObservationError).boundary === expected);
    assert.equal(calls, nativeCalls);
  }
  let live = true, rawReads = 0;
  await assert.rejects(requestNativeObservations({...request(response()), isCurrent: () => live,
    bridge: {observe: async () => {
      live = false;
      throw {get code() {rawReads++; return 'layout_observation_scope';},
        get message() {rawReads++; return 'private native detail';}};
    }}}), (error: unknown) => failure('scope-changed')(error)
      && (error as NativeObservationError).boundary === 'js-after-native');
  assert.equal(rawReads, 0);
});

test('native scope stages accept only the agreed scalar userInfo while JS remains current', async () => {
  const stages = ['admission', 'ui-current', 'root-resolve', 'root-attachment', 'root-walk',
    'pre-observation-scope', 'observation', 'post-observation-scope', 'response'];
  let reads = 0;
  for (const stage of stages.flatMap(value => [value, `${value}-exception`])) {
    await assert.rejects(requestNativeObservations({...request(response()), bridge: {observe: async () => {
      throw {code: 'layout_observation_scope', userInfo: {nativeObservationStage: stage,
        get props() {reads++; throw new Error('private native detail');}},
      get message() {reads++; throw new Error('private native detail');}};
    }}}), (error: unknown) => failure('scope-changed')(error)
      && (error as NativeObservationError).boundary === 'native-scope-rejected-while-js-current'
      && (error as NativeObservationError).nativeObservationStage === stage);
  }
  for (const userInfo of [undefined, null, 'observation', {nativeObservationStage: '/private/detail'},
    {nativeObservationStage: 'root-resolve-exception-extra'}, {nativeObservationStage: 1},
    {get nativeObservationStage() {reads++; throw new Error('private native detail');}}]) {
    await assert.rejects(requestNativeObservations({...request(response()), bridge: {observe: async () => {
      throw {code: 'layout_observation_scope', userInfo};
    }}}), (error: unknown) => failure('scope-changed')(error)
      && (error as NativeObservationError).nativeObservationStage === undefined);
  }
  let live = true;
  await assert.rejects(requestNativeObservations({...request(response()), isCurrent: () => live, bridge: {observe: async () => {
    live = false;
    throw {code: 'layout_observation_scope', get userInfo() {reads++; return {nativeObservationStage: 'observation'};}};
  }}}), (error: unknown) => failure('scope-changed')(error)
    && (error as NativeObservationError).boundary === 'js-after-native'
    && (error as NativeObservationError).nativeObservationStage === undefined);
  assert.equal(reads, 0);
});

test('native first-false scope names stay bounded to current nonexception scope stages', async () => {
  const checks = ['activity-intent-current', 'root-tag-current', 'root-attached', 'decor-attached',
    'decor-current', 'root-window-current', 'decor-window-current', 'root-in-decor', 'fixture-found',
    'fixture-tag-current', 'fixture-attached', 'fixture-window-current', 'fixture-in-root'];
  const reject = (userInfo: unknown, current = () => true) => requestNativeObservations({...request(response()),
    isCurrent: current, bridge: {observe: async () => {throw {code: 'layout_observation_scope', userInfo};}}});
  for (const stage of ['pre-observation-scope', 'post-observation-scope']) {
    for (const check of checks) {
      await assert.rejects(reject({nativeObservationStage: stage, nativeObservationScopeCheck: check}),
        (error: unknown) => failure('scope-changed')(error)
          && (error as NativeObservationError).nativeObservationScopeCheck === check);
    }
  }
  for (const stage of [undefined, 'observation', 'root-walk', 'pre-observation-scope-exception', 'post-observation-scope-exception']) {
    await assert.rejects(reject({nativeObservationStage: stage, nativeObservationScopeCheck: 'fixture-found'}),
      (error: unknown) => failure('scope-changed')(error)
        && (error as NativeObservationError).nativeObservationScopeCheck === undefined);
  }
  let reads = 0;
  for (const userInfo of [{nativeObservationScopeCheck: '/private/detail'}, {nativeObservationScopeCheck: 0},
    {nativeObservationScopeCheck: 'fixture-found-exception'},
    {get nativeObservationScopeCheck() {reads++; throw new Error('private native detail');}}]) {
    Object.defineProperty(userInfo, 'nativeObservationStage', {value: 'pre-observation-scope'});
    await assert.rejects(reject(userInfo),
      (error: unknown) => failure('scope-changed')(error)
        && (error as NativeObservationError).nativeObservationScopeCheck === undefined);
  }
  let live = true;
  await assert.rejects(requestNativeObservations({...request(response()), isCurrent: () => live, bridge: {observe: async () => {
    live = false;
    throw {code: 'layout_observation_scope', get userInfo() {reads++; throw new Error('private native detail');}};
  }}}), (error: unknown) => failure('scope-changed')(error)
    && (error as NativeObservationError).nativeObservationScopeCheck === undefined);
  assert.equal(reads, 0);
});

test('fixture retirement and abort discard native results and fence the queued call', async () => {
  let live = true, settle!: (raw: unknown) => void, calls = 0;
  const pending = requestNativeObservations({...request(response()), isCurrent: () => live,
    bridge: {observe: () => {calls++; return new Promise(resolve => {settle = resolve;});}}});
  await Promise.resolve(); live = false; settle(response());
  await assert.rejects(pending, failure('scope-changed'));
  const controller = new AbortController();
  const aborted = requestNativeObservations({...request(response()), signal: controller.signal,
    bridge: {observe: async () => {calls++; return response();}}});
  controller.abort();
  await assert.rejects(aborted, failure('cancelled'));
  assert.equal(calls, 1);
});

test('in-flight cancellation retires publication without claiming the native UI turn stopped', async () => {
  const controller = new AbortController();
  let settle!: (raw: unknown) => void, finished = false;
  const pending = requestNativeObservations({...request(response()), signal: controller.signal,
    bridge: {observe: () => new Promise(resolve => {settle = value => {finished = true; resolve(value);};})}});
  await Promise.resolve(); controller.abort();
  await assert.rejects(pending, failure('cancelled'));
  assert.equal(finished, false);
  settle(response());
  await Promise.resolve();
  assert.equal(finished, true);
});

test('request tag identity is captured before asynchronous native work', async () => {
  const base = request(response());
  let observed: unknown;
  base.bridge.observe = async (...args: unknown[]) => {observed = args; return response();};
  const pending = requestNativeObservations(base);
  base.rootReactTag = 99; base.fixtureReactTag = 98; base.hostReactTags.push(999);
  await pending;
  assert.deepEqual(observed, [11, 12, [11, 12, 13]]);
});

function scene(origin = 0) {
  const title = {nodeType: 1, nodeName: 'RN:Text', isConnected: true, childNodes: [],
    getBoundingClientRect: () => ({x: 10, y: 20 + origin, width: 100, height: 30})};
  const fixture = {nodeType: 1, nodeName: 'RN:View', isConnected: true, childNodes: [title],
    getBoundingClientRect: () => ({x: 0, y: origin, width: 390, height: 844})};
  const root = {nodeType: 1, nodeName: 'RN:View', isConnected: true, childNodes: [fixture],
    getBoundingClientRect: () => ({x: 0, y: origin, width: 390, height: 844}),
    measureInWindow: (callback: (x: number, y: number, width: number, height: number) => void) => callback(0, 0, 390, 844)};
  const registry = createNativeMeasurementRegistry();
  const scope = registry.register(fixture, {kind: 'view', semanticId: 'accounts-welcome'});
  registry.register(title, {kind: 'text', semanticId: 'public-title', flattenedStyle: {fontSize: 99, color: 'red'}})
    .recordTextLayout([{text: 'Public Welcome', x: 0, y: 0, width: 100, height: 30}]);
  const tags = new Map<object, number>([[root, 11], [fixture, 12], [title, 13]]);
  const facts = {isFocused: native(false), effectiveOpacity: native(1),
    backgroundColor: native('rgba(0, 0, 0, 1)'), effectiveBackgroundColor: native('rgba(0, 0, 0, 1)'),
    borderRadius: native(0), boxShadow: {unavailable: 'unsupported-shadow'},
    textColor: native('rgba(237, 245, 250, 1)'), fontSize: native(24), fontWeight: native(700), lineHeight: native(30),
    fontFamily: native('Manrope ExtraBold'), textLineRects: native([{x: 10, y: 20, width: 100, height: 30}])};
  const payload = {version: 1, unit: 'dp', rootReactTag: 11, fixtureReactTag: 12,
    hosts: [11, 12, 13].map(reactTag => ({reactTag, facts: {...facts,
      windowRect: {value: reactTag === 13 ? {x: 10, y: 20, width: 100, height: 30} : {x: 0, y: 0, width: 390, height: 844},
        source: 'android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp'}}}))};
  const observer = createNativeTreeObserver({bridge: {observe: async () => payload}, tagForHost: host => tags.get(host)});
  const options = {root, registry, observe: observer, isCurrent: () => true,
    viewport: {width: 390, height: 844, deviceScaleFactor: 3}, platform: 'android' as const};
  return {root, fixture, title, registry, scope, payload, observer, options};
}

test('collector uses observed native paint and same-window runs without promoting diagnostics or unassociated family labels', async () => {
  const {options} = scene();
  const measured = await collectNativeScreen(options);
  const title = measured.elements.find(element => element.tag === 'RN:Text')!;
  assert.equal(measured.fullViewport, true);
  assert.deepEqual(title.style, {backgroundColor: 'rgba(0, 0, 0, 1)', color: 'rgba(237, 245, 250, 1)',
    fontSize: '24px', lineHeight: '30px', borderRadius: '0px', fontWeight: '700'});
  assert.deepEqual(title.diagnostics, {flattenedStyle: {fontSize: 99, color: 'red'},
    nativeWindowRect: {x: 10, y: 20, width: 100, height: 30},
    publicMeasurement: {api: 'bounding-client-rect', rect: {x: 10, y: 20, width: 100, height: 30}},
    nativeWindowTextRects: [{x: 10, y: 20, width: 100, height: 30}]});
  assert.equal(title.ownText, 'Public Welcome');
  assert.deepEqual(title.textRects, [{x: 10, y: 20, width: 100, height: 30}]);
  assert.equal(title.focused, false);
  assert.equal('fontFamily' in (title.style as object), false);
  assert.ok(measured.unavailable.some(fact => fact.element === title.id && fact.property === 'resolvedFontFamily'));
  assert.ok(measured.unavailable.some(fact => fact.element === title.id && fact.property === 'boxShadow'));
  for (const property of ['fontStyle', 'letterSpacing', 'textTransform', 'textDecorationLine', 'textShadow']) {
    assert.equal((title.style as Record<string, unknown>)[property], undefined);
    assert.ok(measured.unavailable.some(fact => fact.element === title.id && fact.property === property));
  }
});

test('the physical source policy preserves disagreeing Fabric diagnostics and native animation geometry', async () => {
  const {options, payload} = scene(24);
  payload.hosts[2].facts.windowRect.value = {x: 35, y: 22, width: 102, height: 32};
  payload.hosts[2].facts.textLineRects = native([{x: 38, y: 23, width: 96, height: 30}]);
  const measured = await collectNativeScreen(options);
  const title = measured.elements.find(element => element.tag === 'RN:Text')!;
  assert.equal(measured.fullViewport, true);
  assert.deepEqual(measured.root, {x: 0, y: 0, width: 390, height: 844});
  assert.equal(title.ownText, 'Public Welcome');
  assert.deepEqual(title.box, {x: 35, y: 22, width: 102, height: 32});
  assert.deepEqual(title.textRects, [{x: 38, y: 23, width: 96, height: 30}]);
  assert.equal(measured.measurement.geometrySource, 'native-window');
  assert.equal(measured.diagnostics.nativeCoordinates.publicRoot?.rect.y, 24);
  assert.equal(measured.diagnostics.nativeCoordinates.publicRootReadback?.rect.y, 0);
  assert.deepEqual((title.diagnostics as {publicMeasurement: unknown}).publicMeasurement,
    {api: 'bounding-client-rect', rect: {x: 10, y: 44, width: 100, height: 30}});
});

test('observed text styles normalize without exposing internal image presence as loaded or fit', async () => {
  const {options, payload} = scene();
  for (const row of payload.hosts) Object.assign(row.facts, {fontStyle: native('italic'), letterSpacing: native(-2.5),
    textDecorationLine: native('underline'), textShadow: native('none'), textTransform: native('uppercase'),
    imageHasImage: native(true), imageScaleType: native('fit-center')});
  const measured = await collectNativeScreen(options);
  const title = measured.elements.find(element => element.tag === 'RN:Text')!;
  assert.equal((title.style as Record<string, string>).fontStyle, 'italic');
  assert.equal((title.style as Record<string, string>).letterSpacing, '-2.5px');
  assert.equal((title.style as Record<string, string>).textDecorationLine, 'underline');
  assert.equal((title.style as Record<string, string>).textShadow, 'none');
  assert.equal((title.style as Record<string, string>).textTransform, undefined);
  assert.equal(JSON.stringify(measured).includes('imageHasImage'), false);
  assert.equal(JSON.stringify(measured).includes('fit-center'), false);
});

test('native scope is the registered welcome root, and unmount during the UI turn discards the whole capture', async () => {
  const {options, scope, payload} = scene();
  let finish!: (value: unknown) => void;
  const observe = createNativeTreeObserver({bridge: {observe: () => new Promise(resolve => {finish = resolve;})},
    tagForHost: host => host === options.root ? 11 : options.registry.read(host)?.kind === 'view' ? 12 : 13});
  const pending = collectNativeScreen({...options, observe});
  for (let index = 0; index < 5 && !finish; index++) await Promise.resolve();
  assert.equal(typeof finish, 'function');
  scope.unregister(); finish(payload);
  await assert.rejects(pending, failure('scope-changed'));
});

test('a nonzero physical window origin stays false even when both public APIs return zero', async () => {
  const {options, payload} = scene();
  for (const row of payload.hosts) {
    row.facts.windowRect.value.y += 24;
    row.facts.textLineRects = native([{x: 10, y: 44, width: 100, height: 30}]);
  }
  const measured = await collectNativeScreen(options);
  assert.equal(measured.fullViewport, false);
  const gap = measured.unavailable.find(item => item.property === 'fullViewport')!;
  assert.match(gap.reason, /coordinate-space=attached-native-window/);
  assert.equal(measured.diagnostics.nativeCoordinates.rootWindow.y, 24);
  assert.equal(measured.diagnostics.nativeCoordinates.publicRoot?.rect.y, 0);
  assert.equal(measured.diagnostics.nativeCoordinates.publicRootReadback?.rect.y, 0);
  assert.deepEqual(measured.elements.find(element => element.tag === 'RN:Text')!.textRects,
    [{x: 10, y: 20, width: 100, height: 30}]);
});

test('missing or duplicate registered fixture roots cannot invoke native observation', async () => {
  const {options, fixture, registry} = scene();
  let calls = 0;
  const observe = async () => {calls++; return new Map();};
  await assert.rejects(collectNativeScreen({...options, observe, isCurrent: undefined}), /current public fixture guard/);
  const other = {...fixture, childNodes: []}; options.root.childNodes.push(other as typeof fixture);
  registry.register(other, {kind: 'view', semanticId: 'tv-front-door'});
  await assert.rejects(collectNativeScreen({...options, observe}), /unique registered public fixture/);
  assert.equal(calls, 0);
});

test('missing physical host bounds become null with a declared gap despite a usable public box', async () => {
  const {options, payload} = scene();
  Object.assign(payload.hosts[2].facts, {windowRect: {unavailable: 'unsupported-transform'}});
  const measured = await collectNativeScreen(options);
  const title = measured.elements.find(element => element.tag === 'RN:Text')!;
  assert.equal(title.box, null);
  assert.equal((title.diagnostics as {nativeWindowRect?: unknown}).nativeWindowRect, undefined);
  assert.equal((title.diagnostics as {nativeWindowTextRects?: unknown}).nativeWindowTextRects, undefined);
  assert.ok((title.diagnostics as {publicMeasurement?: unknown}).publicMeasurement);
  assert.ok(measured.unavailable.some(fact => fact.element === title.id && fact.property === 'box'));
});

test('missing native root geometry or observer refuses rather than emitting a public-layout fallback', async () => {
  const {options, payload} = scene();
  await assert.rejects(collectNativeScreen({...options, observe: undefined}), /scoped native-window observer is required/);
  Object.assign(payload.hosts[0].facts, {windowRect: {unavailable: 'unsupported-transform'}});
  await assert.rejects(collectNativeScreen(options), /native-window root bounds are unavailable/);
});

test('production observer construction never loads native modules', () => {
  const globals = globalThis as typeof globalThis & {__DEV__?: boolean};
  const before = globals.__DEV__;
  try {globals.__DEV__ = false; assert.equal(createAndroidNativeTreeObserver(), undefined);}
  finally {if (before === undefined) delete globals.__DEV__; else globals.__DEV__ = before;}
});
