import {collectReadingHighlights, type ReadingHighlightsFile} from './reading-highlights-export.ts';
import type {ReadingManifest, ReadingProgress, ReadingSavedProgress, ReadingSession, ReadingPage} from './reading.ts';
import {captureReadingAnnotationMutation, parseReadingAnnotations, parseReadingAnnotationSaved} from './reading-annotations.ts';
import {ReadingError, parseReadingManifest, validateReadingLocation} from './reading.ts';
import {parsePreferenceSnapshot, type PreferenceSnapshot} from './preferences.ts';
import {decodeGetReadingSessionsSessionIdProgressResponse, decodePutReadingSessionsSessionIdProgressResponse} from '../../contracts/src/index.ts';
import {validReadingIndexedSearchQuery} from './reading-indexed-search.ts';

/** Local trusted-app bridge only. No HTTP path, origin, credential or fetch port. */
export type ReadingHostSession = Pick<ReadingSession, 'manifest' | 'signal' | 'headPDF' | 'readRange' | 'readAsset' | 'page' | 'progress' | 'saveProgress'> & Partial<Pick<ReadingSession, 'openSequential' | 'annotations' | 'mutateAnnotation'>>;
export type ReadingHostTransport = Readonly<{send: (message: string) => void; subscribe: (listener: (message: string) => void) => () => void}>;
export type ReadingHostContentsTarget = Readonly<{sourceId: string; sourceRevision: string; index: number}>;
/** Trusted native navigation only. True means that this original navigation was
 * accepted; it conveys no search result, source permission or reader location. */
export type ReadingHostSearchAll = (query: string, signal: AbortSignal, isCurrent: () => boolean) => boolean | Promise<boolean>;
/** Window-local vertical hinge band from the real platform posture, when reported. */
export type ReadingHostCrease = Readonly<{left: number; right: number}>;
type RecordValue = Record<string, unknown>;
const record = (v: unknown): v is RecordValue => typeof v === 'object' && v !== null && !Array.isArray(v);
const integer = (v: unknown, min: number, max: number): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max;
function fail(code = 'invalid_bridge'): never {throw new ReadingError(code);}
const CHUNK = 48 * 1024, CONTROL = 2 * 1024 * 1024;
const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
function encode(bytes: Uint8Array): string {
  let out = '';
  for (let i = 0; i < bytes.length; i += 3) {
    const a = bytes[i]!, b = bytes[i + 1] ?? 0, c = bytes[i + 2] ?? 0;
    out += alphabet[a >> 2]! + alphabet[((a & 3) << 4) | (b >> 4)]! + (i + 1 < bytes.length ? alphabet[((b & 15) << 2) | (c >> 6)] : '=') + (i + 2 < bytes.length ? alphabet[c & 63] : '=');
  }
  return out;
}
function decode(text: unknown, expected: number): Uint8Array {
  if (typeof text !== 'string' || text.length !== Math.ceil(expected / 3) * 4 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(text)) fail();
  const out = new Uint8Array(expected); let offset = 0;
  for (let i = 0; i < text.length; i += 4) {
    const a = alphabet.indexOf(text[i]!), b = alphabet.indexOf(text[i + 1]!), c = alphabet.indexOf(text[i + 2]!), d = alphabet.indexOf(text[i + 3]!);
    if (offset < expected) out[offset++] = (a << 2) | (b >> 4);
    if (offset < expected) out[offset++] = ((b & 15) << 4) | (c >> 2);
    if (offset < expected) out[offset++] = ((c & 3) << 6) | d;
  }
  return out;
}
function packet(message: string, max: number): RecordValue {
  if (typeof message !== 'string' || message.length > max) fail();
  const value: unknown = JSON.parse(message);
  if (!record(value) || value.v !== 1) fail();
  return value;
}
function frozen<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) frozen(child); Object.freeze(value);}
  return value;
}
function errorInfo(error: unknown) {
  return error instanceof ReadingError ? {code: error.code, status: error.status} : {code: 'reading_unavailable'};
}

/** Intentional original-host retirement is cancellation, while a typed
 * Reading failure keeps the source/permission reason the session supplied. */
function retirementErrorInfo(reason: unknown) {
  if(reason instanceof ReadingError)return errorInfo(reason);
  if(reason==null||record(reason)&&reason.name==='AbortError')return {code:'cancelled'};
  return errorInfo(reason);
}

/** Optional layout fact; a missing value is the ordinary no-hinge window. */
function creaseOf(value: unknown): ReadingHostCrease | undefined {
  if (value === undefined) return;
  if (!record(value) || typeof value.left !== 'number' || typeof value.right !== 'number' || !Number.isFinite(value.left) || !Number.isFinite(value.right)
    || value.left <= 0 || value.right < value.left || value.right - value.left > 120) fail('invalid_bridge');
  return Object.freeze({left: value.left, right: value.right});
}
/** A contents index is meaningful only for this exact opened publication. */
function contentsTarget(value: unknown, manifest: ReadingManifest): ReadingHostContentsTarget | undefined {
  if (value === undefined) return;
  if (!record(value) || manifest.format !== 'epub' || value.sourceId !== manifest.sourceId || value.sourceRevision !== manifest.revision
    || !integer(value.index, 0, 511) || value.index >= (manifest.contents?.length ?? 0)) fail('invalid_bridge');
  return Object.freeze({sourceId: manifest.sourceId, sourceRevision: manifest.revision, index: value.index});
}

/** RN retains the actual captured facade. Its caller still owns session.close().
 * Binary messages are pulled in <=48KiB chunks; just one original member/page is
 * retained, with an idle deadline, never a book/archive cache. */
export function createReadingHost({session, channel, preferences, initialProgress, initialContentsTarget, crease, current, send, onBack, onSettings, onSearchAll, onUnavailable, saveHighlights}: Readonly<{
  saveHighlights?: (file: ReadingHighlightsFile, signal: AbortSignal, isCurrent: () => boolean) => Promise<'saved' | 'cancelled'>;
  session: ReadingHostSession; channel: string; preferences: () => PreferenceSnapshot; initialProgress: ReadingProgress;
  initialContentsTarget?: ReadingHostContentsTarget; crease?: ReadingHostCrease; onUnavailable?: () => void;
  onSearchAll?: ReadingHostSearchAll;
  current: () => boolean; send: (message: string) => void; onBack: () => void; onSettings: () => void;
}>) {
  if (!/^[A-Za-z0-9_-]{16,128}$/.test(channel)) fail();
  const manifest = session.manifest, life = new AbortController(), requests = new Map<number, AbortController>();
  const capturedContentsTarget = contentsTarget(initialContentsTarget, manifest);
  let capturedCrease: ReadingHostCrease | undefined;
  try {capturedCrease = creaseOf(crease);} catch {capturedCrease = undefined;}
  let started = false, active = true, last = 0, binaryRequest = false, transfer: {id: number; bytes: Uint8Array; offset: number; timer: ReturnType<typeof setTimeout>} | undefined;
  type Sequential = {id: number; life: AbortController; reader?: Pick<ReadableStreamDefaultReader<Uint8Array>, 'read' | 'cancel' | 'releaseLock'>; total: number; pulling: boolean; timer?: ReturnType<typeof setTimeout>};
  let sequential: Sequential | undefined;
  let highlightsRequest: AbortController | undefined;
  let searchAllRequest: AbortController | undefined;
  const valid = () => {try {return !life.signal.aborted && !session.signal.aborted && current() && session.manifest === manifest;} catch {return false;}};
  const check = () => {if (!valid()) fail('cancelled');};
  const post = (value: RecordValue) => {if (valid()) send(JSON.stringify({v: 1, channel, ...value}));};
  const release = () => {if (transfer) clearTimeout(transfer.timer); transfer = undefined;};
  const stopSequential = (value: Sequential, cancel = true) => {
    if (sequential !== value) return;
    sequential = undefined; clearTimeout(value.timer); value.life.abort();
    const reader = value.reader;
    if (reader) {
      const unlock = () => {try {reader.releaseLock();} catch { /* cancelled pending read releases below */ }};
      if (cancel) void reader.cancel().catch(() => {}).finally(unlock); else unlock();
    }
  };
  const touchSequential = (value: Sequential) => {
    clearTimeout(value.timer); value.timer = setTimeout(() => stopSequential(value), 30000);
  };
  const dispose = () => {
    if (life.signal.aborted) return;
    // Retirement is local; it must not issue a cleanup request as another viewer.
    try {send(JSON.stringify({v: 1, channel, kind: 'retired', error: retirementErrorInfo(session.signal.reason)}));} catch { /* native surface already gone */ }
    life.abort(); release(); if (sequential) stopSequential(sequential); for (const request of requests.values()) request.abort(); requests.clear();
    session.signal.removeEventListener('abort', dispose);
  };
  session.signal.addEventListener('abort', dispose, {once: true});
  if (session.signal.aborted) dispose();
  const stage = (id: number, bytes: Uint8Array, metadata: RecordValue = {}) => {
    if (transfer || bytes.byteLength > 32 * 1024 * 1024) fail('bridge_busy');
    transfer = {id, bytes, offset: 0, timer: setTimeout(release, 30000)};
    return {transfer: id, length: bytes.byteLength, ...metadata};
  };
  const receive = (message: string) => {
    let data: RecordValue;
    try {
      data = packet(message, 65536);
      if (data.kind === 'request' && data.method === 'annotation-save') {
        if (Object.keys(data).some(key => !['v','kind','channel','id','method','args'].includes(key)) || !record(data.args)
          || new TextEncoder().encode(JSON.stringify(data.args)).byteLength > 32768) fail();
      } else if (data.kind === 'request' && data.method === 'search-all') {
        if (message.length > 16384 || Object.keys(data).length !== 6
          || Object.keys(data).some(key => !['v','kind','channel','id','method','args'].includes(key))
          || !record(data.args) || Object.keys(data.args).length !== 1 || !Object.hasOwn(data.args, 'query')
          || !validReadingIndexedSearchQuery(data.args.query)) fail();
      } else if (message.length > 16384) fail();
    } catch {return;}
    if (data.kind === 'ready') {
      if (started || !valid()) return;
      if (capturedContentsTarget && data.contentsTarget !== true) {dispose(); onUnavailable?.(); return;}
      started = true; post({kind: 'initial', canSaveHighlights: !!saveHighlights && manifest.canAnnotate === true && !!session.annotations && !!session.mutateAnnotation, canSearchAll: typeof onSearchAll === 'function', manifest: {...manifest, canAnnotate:manifest.canAnnotate === true && !!session.annotations && !!session.mutateAnnotation ? true : undefined}, preferences: preferences(), progress: initialProgress, active,
        ...(capturedContentsTarget ? {contentsTarget: capturedContentsTarget} : {}), ...(capturedCrease ? {crease: capturedCrease} : {})}); return;
    }
    if (!started || data.channel !== channel || !valid()) return;
    if (data.kind === 'sequential-cancel' && sequential && data.stream === sequential.id) {stopSequential(sequential); release(); return;}
    if (data.kind === 'cancel' && integer(data.id, 1, last)) {requests.get(data.id)?.abort(); if (transfer?.id === data.id) release(); return;}
    if (data.kind !== 'request' || !integer(data.id, last + 1, Number.MAX_SAFE_INTEGER)) return;
    last = data.id; const id = data.id, method = data.method, args = record(data.args) ? data.args : {};
    if (requests.size >= 4) {post({kind: 'reply', id, error: {code: 'bridge_busy'}}); return;}
    const request = new AbortController(); requests.set(id, request);
    let ownsBinary = false;
    const deadline = setTimeout(() => request.abort(), 30000);
    void (async () => {
      check(); let value: unknown;
      if (method === 'range' || method === 'asset' || method === 'page') {if (binaryRequest || transfer) fail('bridge_busy'); binaryRequest = true; ownsBinary = true;}
      if (method === 'search-all') {
        if (!active || !onSearchAll) fail('not_permitted');
        if (!validReadingIndexedSearchQuery(args.query)) fail();
        if (searchAllRequest) fail('bridge_busy');
        searchAllRequest = request;
        const live = () => valid() && active && !request.signal.aborted;
        if (!live()) fail('cancelled');
        const accepted = await onSearchAll(args.query, request.signal, live);
        if (!live() || accepted !== true) fail('cancelled');
        value = true;
      } else if (method === 'highlights-export') {
        if (!active || !saveHighlights || manifest.canAnnotate !== true || !session.annotations || !session.mutateAnnotation || Object.keys(args).some(key => key !== 'revision') || !integer(args.revision, 0, Number.MAX_SAFE_INTEGER)) fail('not_permitted');
        if (highlightsRequest) fail('bridge_busy');
        highlightsRequest = request;
        const revision = args.revision;
        const live = () => valid() && active && !request.signal.aborted;
        const port = {manifest, signal: session.signal, annotations: session.annotations, mutateAnnotation: session.mutateAnnotation};
        const file = await collectReadingHighlights({port, revision, title: manifest.metadata.title, author: manifest.metadata.author, signal: request.signal, isCurrent: live});
        if (!live()) fail('cancelled');
        value = await saveHighlights(file, request.signal, live);
        if (!live()) fail('cancelled');
        if (value !== 'saved' && value !== 'cancelled') fail();
      } else if (method === 'head') value = await session.headPDF(request.signal) ?? null;
      else if (method === 'range') {
        if (manifest.sequential) fail('invalid_format');
        if (!integer(args.start, 0, manifest.size - 1) || !integer(args.endExclusive, Number(args.start) + 1, manifest.size) || args.endExclusive - args.start > 65536) fail('invalid_range');
        if (transfer) fail('bridge_busy');
        value = stage(id, await session.readRange({start: args.start, endExclusive: args.endExclusive}, request.signal));
      } else if (method === 'asset') {
        if (typeof args.path !== 'string' || args.path.length > 4096 || transfer) fail('invalid_asset');
        value = stage(id, await session.readAsset(args.path, request.signal));
      } else if (method === 'page') {
        if (!integer(args.index, 0, 4095) || !integer(args.width, 32, 2048) || transfer) fail('invalid_page');
        const page = await session.page(args.index, {width: args.width, signal: request.signal});
        value = stage(id, page.bytes, {index: page.index, name: page.name, mime: page.mime});
      } else if (method === 'sequential-open') {
        if (manifest.format !== 'pdf' || manifest.sequential !== true || !session.openSequential) fail('invalid_format');
        if (sequential || binaryRequest || transfer) fail('sequential_busy');
        const stream: Sequential = {id, life: new AbortController(), total: 0, pulling: false}; sequential = stream;
        const aborted = () => stopSequential(stream); request.signal.addEventListener('abort', aborted, {once: true});
        try {
          const body = await session.openSequential(stream.life.signal);
          if (sequential !== stream || stream.life.signal.aborted || request.signal.aborted || !valid()) {
            void body.cancel().catch(() => {}); fail('cancelled');
          }
          stream.reader = body.getReader(); touchSequential(stream); value = {stream: stream.id};
        } catch (error) {stopSequential(stream); throw error;}
        finally {request.signal.removeEventListener('abort', aborted);}
      } else if (method === 'sequential-read') {
        const stream = sequential;
        if (!stream || args.stream !== stream.id || !stream.reader) fail('cancelled');
        if (stream.pulling || binaryRequest || transfer) fail('bridge_busy');
        stream.pulling = true; binaryRequest = true; ownsBinary = true; touchSequential(stream);
        const aborted = () => stopSequential(stream); request.signal.addEventListener('abort', aborted, {once: true});
        try {
          const next = await stream.reader.read();
          check(); if (sequential !== stream || request.signal.aborted) fail('cancelled');
          if (next.done) {
            if (stream.total !== manifest.size) fail('response_size');
            stopSequential(stream, false); value = {stream: stream.id, done: true};
          } else {
            if (!(next.value instanceof Uint8Array) || next.value.byteLength > 1 << 20 || stream.total + next.value.byteLength > manifest.size) fail('response_size');
            stream.total += next.value.byteLength;
            value = stage(id, next.value, {stream: stream.id, done: false});
          }
        } catch (error) {stopSequential(stream); throw error;}
        finally {stream.pulling = false; request.signal.removeEventListener('abort', aborted);}
      } else if (method === 'chunk') {
        if (!transfer || args.transfer !== transfer.id || args.offset !== transfer.offset) fail();
        if (sequential) touchSequential(sequential);
        const start = transfer.offset, end = Math.min(start + CHUNK, transfer.bytes.length);
        value = {offset: start, bytes: encode(transfer.bytes.subarray(start, end))}; transfer.offset = end;
        clearTimeout(transfer.timer);
        if (end === transfer.bytes.length) release(); else transfer.timer = setTimeout(release, 30000);
      } else if (method === 'release') {if (transfer?.id === args.transfer) release(); value = null;}
      else if (method === 'progress') value = await session.progress(request.signal);
      else if (method === 'annotations') {
        if (manifest.canAnnotate !== true || !session.annotations || typeof args.cursor !== 'string' || args.cursor.length > 64 || args.revision !== undefined && !integer(args.revision,0,Number.MAX_SAFE_INTEGER)) fail('not_permitted');
        value = await session.annotations(args.cursor, args.revision as number | undefined, request.signal);
      } else if (method === 'annotation-save') {
        if (manifest.canAnnotate !== true || !session.mutateAnnotation) fail('not_permitted');
        value = await session.mutateAnnotation(captureReadingAnnotationMutation(args, location => validateReadingLocation(location,manifest)), request.signal);
      } else if (method === 'save') {
        // The canonical facade validates the generated location and CAS request before I/O.
        if (!integer(args.expectedRevision, 0, Number.MAX_SAFE_INTEGER) || !record(args.location)) fail('invalid_location');
        value = await session.saveProgress(args as Parameters<ReadingSession['saveProgress']>[0], request.signal);
      } else if (method === 'back') {onBack(); value = null;}
      else if (method === 'settings') {onSettings(); value = null;}
      else fail();
      check(); if (request.signal.aborted) {if (transfer?.id === id) release(); fail('cancelled');}
      if (ownsBinary) {binaryRequest = false; ownsBinary = false;}
      post({kind: 'reply', id, value});
    })().catch(error => {if (transfer?.id === id) release(); post({kind: 'reply', id, error: errorInfo(error)});})
      .finally(() => {if (highlightsRequest === request) highlightsRequest = undefined; if (searchAllRequest === request) searchAllRequest = undefined; clearTimeout(deadline); if (ownsBinary) binaryRequest = false; requests.delete(id);});
  };
  return Object.freeze({receive, dispose, confirm: () => {if (started) post({kind: 'preferences', preferences: preferences()});},
    setActive: (value: boolean) => {active = value; if (!active) {highlightsRequest?.abort(); searchAllRequest?.abort();} if (started) post({kind: 'active', active});},
    setCrease: (value: ReadingHostCrease | undefined) => {
      try {capturedCrease = creaseOf(value);} catch {capturedCrease = undefined;}
      if (started) post({kind: 'layout', ...(capturedCrease ? {crease: capturedCrease} : {})});
    }});
}

export type ReadingGuest = Readonly<{
  session: ReadingHostSession; initialProgress: ReadingProgress; contentsTarget?: ReadingHostContentsTarget;
  /** The actual window fold, updated by the host when the posture changes. */
  readonly crease?: ReadingHostCrease; onCrease: (listener: () => void) => () => void;
  preferences: Readonly<{getSnapshot: () => PreferenceSnapshot; subscribe: (listener: () => void) => () => void}>;
  onActive: (listener: (active: boolean) => void) => () => void;
  saveHighlights?: (revision: number, signal: AbortSignal) => Promise<'saved' | 'cancelled'>;
  /** Present only when the original native host supplies the navigation port. */
  searchAll?: (query: string, signal?: AbortSignal) => Promise<void>;
  back: () => Promise<void>; openSettings: () => Promise<void>; dispose: () => void;
}>;
/** Bundled browser side: structural source ports only, no selected-server API. */
export function connectReadingHost(transport: ReadingHostTransport): Readonly<{ready: Promise<ReadingGuest>; dispose: () => void}> {
  const life = new AbortController(), waiting = new Map<number, {resolve: (value: unknown) => void; reject: (error: unknown) => void}>();
  const listeners = new Set<() => void>(), activeListeners = new Set<(active: boolean) => void>();
  let cancelSequential: ((reason: unknown) => void) | undefined;
  let channel: string | undefined, sequence = 0, snapshot: PreferenceSnapshot, initialized = false, active = true, binaryTail = Promise.resolve(), crease: ReadingHostCrease | undefined;
  const creaseListeners = new Set<() => void>();
  let resolveReady!: (guest: ReadingGuest) => void, rejectReady!: (error: unknown) => void;
  const ready = new Promise<ReadingGuest>((resolve, reject) => {resolveReady = resolve; rejectReady = reject;});
  void ready.catch(() => {});
  const dispose = (reason: unknown = new ReadingError('cancelled')) => {
    if (life.signal.aborted) return;
    cancelSequential?.(reason);
    life.abort(reason); clearTimeout(startDeadline); off(); rejectReady(reason);
    for (const pending of waiting.values()) pending.reject(reason);
    waiting.clear(); listeners.clear(); activeListeners.clear(); creaseListeners.clear();
  };
  function call(method: string, args: RecordValue = {}, signal?: AbortSignal): Promise<unknown> {
    if (!channel || life.signal.aborted || signal?.aborted) return Promise.reject(new ReadingError('cancelled'));
    if (waiting.size >= 4) return Promise.reject(new ReadingError('bridge_busy'));
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout>;
      const release = () => {clearTimeout(timer); signal?.removeEventListener('abort', aborted); waiting.delete(id);};
      const aborted = () => {release(); transport.send(JSON.stringify({v: 1, channel, kind: 'cancel', id})); reject(new ReadingError('cancelled'));};
      waiting.set(id, {resolve(value) {release(); resolve(value);}, reject(error) {release(); reject(error);}});
      timer = setTimeout(aborted, 30000); signal?.addEventListener('abort', aborted, {once: true});
      try {transport.send(JSON.stringify({v: 1, channel, kind: 'request', id, method, args}));} catch (error) {release(); reject(error);}
    });
  }
  async function binary(method: string, args: RecordValue, signal?: AbortSignal): Promise<{bytes: Uint8Array; metadata: RecordValue}> {
    const predecessor = binaryTail; let unlock!: () => void;
    binaryTail = new Promise<void>(resolve => {unlock = resolve;});
    let transfer: number | undefined;
    try {
      await predecessor;
      if (life.signal.aborted || signal?.aborted) fail('cancelled');
      const metadata = await call(method, args, signal);
      if (method === 'sequential-read' && record(metadata) && metadata.done === true) return {bytes: new Uint8Array(), metadata};
      if (!record(metadata) || !integer(metadata.transfer, 1, Number.MAX_SAFE_INTEGER) || !integer(metadata.length, 0, method === 'sequential-read' ? 1 << 20 : 32 * 1024 * 1024)) fail();
      transfer = metadata.transfer; const bytes = new Uint8Array(metadata.length);
      for (let offset = 0; offset < bytes.length;) {
        const size = Math.min(CHUNK, bytes.length - offset), part = await call('chunk', {transfer, offset}, signal);
        if (!record(part) || part.offset !== offset) fail();
        bytes.set(decode(part.bytes, size), offset); offset += size;
      }
      return {bytes, metadata};
    } finally {if (transfer !== undefined && !life.signal.aborted) void call('release', {transfer}).catch(() => {}); unlock();}
  }
  async function openSequential(manifest: ReadingManifest, signal?: AbortSignal): Promise<ReadableStream<Uint8Array>> {
    if (manifest.format !== 'pdf' || manifest.sequential !== true) fail('invalid_format');
    if (life.signal.aborted || signal?.aborted) fail('cancelled');
    if (cancelSequential) fail('sequential_busy');
    const child = new AbortController();
    let stream: number | undefined, ended = false, total = 0, controller: ReadableStreamDefaultController<Uint8Array> | undefined;
    const cancelRemote = () => {if (stream !== undefined && !life.signal.aborted) {
      try {transport.send(JSON.stringify({v: 1, channel, kind: 'sequential-cancel', stream}));} catch { /* host already retired */ }
    }};
    const end = (reason?: unknown, consumerCancel = false, eof = false) => {
      if (ended) return;
      ended = true; child.abort(); signal?.removeEventListener('abort', aborted);
      if (cancelSequential === retire) cancelSequential = undefined;
      if (!eof) cancelRemote();
      if (!consumerCancel && controller) {if (eof) controller.close(); else controller.error(reason ?? new ReadingError('cancelled'));}
    };
    const retire = (reason: unknown) => end(reason);
    const aborted = () => end(new ReadingError('cancelled'));
    cancelSequential = retire; signal?.addEventListener('abort', aborted, {once: true});
    try {
      const opened = await call('sequential-open', {}, child.signal);
      if (!record(opened) || !integer(opened.stream, 1, Number.MAX_SAFE_INTEGER)) fail();
      stream = opened.stream;
      if (ended || life.signal.aborted || signal?.aborted) {cancelRemote(); fail('cancelled');}
      return new ReadableStream<Uint8Array>({
        start(value) {controller = value;},
        async pull() {
          if (ended) return;
          try {
            const next = await binary('sequential-read', {stream}, child.signal);
            if (ended) return;
            if (next.metadata.stream !== stream || typeof next.metadata.done !== 'boolean') fail();
            if (next.metadata.done) {if (total !== manifest.size) fail('response_size'); end(undefined, false, true); return;}
            if (total + next.bytes.byteLength > manifest.size) fail('response_size');
            total += next.bytes.byteLength; controller!.enqueue(next.bytes);
          } catch (error) {end(error);}
        },
        cancel() {end(undefined, true);},
      }, {highWaterMark: 0});
    } catch (error) {end(error); throw error;}
  }
  const off = transport.subscribe(message => {
    try {
      const data = packet(message, CONTROL);
      if (data.kind === 'initial' && !initialized) {
        if (typeof data.channel !== 'string' || !/^[A-Za-z0-9_-]{16,128}$/.test(data.channel)) fail();
        const manifest: ReadingManifest = parseReadingManifest(data.manifest);
        const initialContentsTarget = contentsTarget(data.contentsTarget, manifest);
        snapshot = parsePreferenceSnapshot(data.preferences);
        const initialProgress = frozen(decodeGetReadingSessionsSessionIdProgressResponse(data.progress));
        let initialCrease: ReadingHostCrease | undefined;
        // Optional layout geometry: an invalid band means the ordinary one-page
        // window, never a broken identity/channel packet.
        try {initialCrease = creaseOf(data.crease);} catch {initialCrease = undefined;}
        crease = initialCrease;
        // Older trusted app hosts omit this optional v1 state. New hosts retain
        // activity through Settings even before the guest has finished loading.
        if (data.active !== undefined && typeof data.active !== 'boolean') fail();
        if (data.canSearchAll !== undefined && typeof data.canSearchAll !== 'boolean') fail();
        active = typeof data.active === 'boolean' ? data.active : true;
        channel = data.channel; initialized = true; clearTimeout(startDeadline);
        const session: ReadingHostSession = Object.freeze({manifest, signal: life.signal,
          headPDF: async signal => {await call('head', {}, signal);},
          readRange: async (range, signal) => (await binary('range', range, signal)).bytes,
          openSequential: signal => openSequential(manifest, signal),
          readAsset: async (path, signal) => (await binary('asset', {path}, signal)).bytes,
          page: async (index, options): Promise<ReadingPage> => {
            const {bytes, metadata} = await binary('page', {index, width: options.width}, options.signal);
            if (metadata.index !== index || typeof metadata.name !== 'string' || metadata.name.length > 4096 || !['image/png', 'image/jpeg', 'image/webp'].includes(String(metadata.mime))) fail();
            return {index, name: metadata.name, mime: metadata.mime as ReadingPage['mime'], bytes};
          },
          ...(manifest.canAnnotate === true ? {
            annotations: async (cursor = '', revision?: number, signal?: AbortSignal) => parseReadingAnnotations(await call('annotations',{cursor,...revision === undefined ? {} : {revision}},signal),manifest,location=>validateReadingLocation(location,manifest),cursor),
            mutateAnnotation: async (input: Parameters<ReadingSession['mutateAnnotation']>[0], signal?: AbortSignal) => {
              const captured=captureReadingAnnotationMutation(input,location=>validateReadingLocation(location,manifest));
              if (new TextEncoder().encode(JSON.stringify(captured)).byteLength > 32768) fail('annotation_limit');
              return parseReadingAnnotationSaved(await call('annotation-save',captured,signal),captured.expectedRevision);
            },
          } : {}),
          progress: async signal => decodeGetReadingSessionsSessionIdProgressResponse(await call('progress', {}, signal)),
          saveProgress: async (request, signal): Promise<ReadingSavedProgress> => decodePutReadingSessionsSessionIdProgressResponse(await call('save', request, signal)),
        });
        resolveReady(Object.freeze({session, initialProgress,
          ...(data.canSearchAll === true ? {searchAll: async (query: string, signal?: AbortSignal): Promise<void> => {
            if (!active || life.signal.aborted || signal?.aborted) fail('cancelled');
            if (!validReadingIndexedSearchQuery(query)) fail('invalid_request');
            const accepted = await call('search-all', {query}, signal);
            if (!active || life.signal.aborted || signal?.aborted) fail('cancelled');
            if (accepted !== true) fail('invalid_bridge');
          }} : {}),
          ...(data.canSaveHighlights === true && manifest.canAnnotate === true ? {saveHighlights: async (revision: number, signal: AbortSignal): Promise<'saved' | 'cancelled'> => {
            if (!integer(revision, 0, Number.MAX_SAFE_INTEGER)) fail();
            const result = await call('highlights-export', {revision}, signal);
            if (result !== 'saved' && result !== 'cancelled') fail();
            return result;
          }} : {}), ...(initialContentsTarget ? {contentsTarget: initialContentsTarget} : {}), get crease() {return crease;},
          onCrease: (listener: () => void) => {if (life.signal.aborted) return () => {}; creaseListeners.add(listener); return () => {creaseListeners.delete(listener);};},
          preferences: Object.freeze({getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}}),
          onActive: (listener: (active: boolean) => void) => {
            if (life.signal.aborted) return () => {};
            activeListeners.add(listener); listener(active); return () => {activeListeners.delete(listener);};
          },
          back: async () => {await call('back');}, openSettings: async () => {await call('settings');}, dispose,
        }));
      } else if (initialized && data.channel === channel) {
        if (data.kind === 'retired') {dispose(new ReadingError(record(data.error) && typeof data.error.code === 'string' ? data.error.code : 'cancelled')); return;}
        if (data.kind === 'preferences') {snapshot = parsePreferenceSnapshot(data.preferences); for (const listener of [...listeners]) listener();}
        else if (data.kind === 'layout') {try {crease = creaseOf(data.crease);} catch {return;} for (const listener of [...creaseListeners]) listener();}
        else if (data.kind === 'active' && typeof data.active === 'boolean') {active = data.active; for (const listener of [...activeListeners]) listener(active);}
        else if (data.kind === 'reply' && integer(data.id, 1, sequence)) {
          const pending = waiting.get(data.id); if (!pending) return;
          if (record(data.error)) pending.reject(new ReadingError(typeof data.error.code === 'string' ? data.error.code : 'reading_unavailable', integer(data.error.status, 100, 599) ? data.error.status : undefined));
          else pending.resolve(data.value);
        }
      }
    } catch {dispose(new ReadingError('invalid_bridge'));}
  });
  const startDeadline = setTimeout(() => dispose(new ReadingError('reading_unavailable')), 30000);
  transport.send(JSON.stringify({v: 1, kind: 'ready', contentsTarget: true}));
  return Object.freeze({ready, dispose});
}
