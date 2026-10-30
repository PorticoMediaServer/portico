import {routeBody} from "../../contracts/src/routes.ts";
import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';
import {wireDurations} from './wire-units.ts';
import type { ContentScope, LibraryContentApi } from './library-content.ts';
export type LyricLine = Readonly<{
    atMs: number | null;
    text: string;
}>;
export type LyricDocument = Readonly<{
    format: 'lrc' | 'text';
    text: string;
    lines: readonly LyricLine[];
    embeddedOffsetMs: number;
}>;
export type LyricProvenance = Readonly<{
    origin: 'upload' | 'sidecar' | 'embedded' | 'lrclib';
    providerId: string;
    label: string;
    rights: string;
}>;
export type LyricRevision = Readonly<{
    id: string;
    revision: number;
    scope: 'library' | 'private';
    canManage: boolean;
    deleted: boolean;
    language: string;
    offsetMs: number;
    format: 'lrc' | 'text';
    digest: string;
    provenance: LyricProvenance;
    createdAt: string;
    document?: LyricDocument;
}>;
export type LyricSource = Readonly<{
    id: string;
    version: string;
    startSeconds: number;
    duration: number;
    sourceDuration: number;
    local: boolean;
}>;
export type LyricsTarget = Readonly<{
    itemId: string;
    libraryId: string;
    sessionId?: string;
    sessionGeneration?: number;
    sourceId?: string;
}>;
export type LyricsView = Readonly<{
    librarySelection?: Readonly<{revision: number; resource: LyricRevision | null}>;
    scope: Readonly<{
        serverId: string;
        libraryId: string;
        itemId: string;
        viewerFence: string;
        sessionId: string;
        sessionGeneration: number;
    }>;
    sources: readonly LyricSource[];
    source: LyricSource | null;
    resources: readonly LyricRevision[];
    selection: Readonly<{
        revision: number;
        resource: LyricRevision | null;
    }>;
    canShare: boolean;
    providers: Readonly<{
        lrclib: boolean;
        configured: boolean;
        revision: number;
    }>;
}>;
export type LyricCandidate = Readonly<{
    id: string;
    language: string;
    format: 'text' | 'lrc';
    provenance: LyricProvenance;
    preview: string;
}>;
export type LyricsSnapshot = Readonly<{
    data: LyricsView | null;
    candidates: readonly LyricCandidate[];
    warnings: readonly string[];
    preview: LyricRevision | null;
    busy: boolean;
    error: string;
    generation: number;
}>;
/** The exact drawn snapshot and the original drawer command, never a later opening. */
export type LyricsChooseContext = Readonly<{ snapshot: LyricsSnapshot; signal: AbortSignal; isCurrent: () => boolean }>;
export type LyricUpload = Readonly<{
    text?: string;
    contentBase64?: string;
    format?: 'text' | 'lrc';
    language: string;
    scope: 'private' | 'library';
    rights?: string;
    candidateId?: string;
    resourceId?: string;
    expectedRevision?: number;
}>;
type LyricsApi = LibraryContentApi & {
    requestLyrics?<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>;
};
const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const str = (v: unknown, max = 1024): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f\ufffd]/.test(v);
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,256}$/.test(v);
const hash = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{64}$/.test(v);
const integer = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
const seconds = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 86400;
const offset = (v: unknown): v is number => Number.isSafeInteger(v) && Math.abs(Number(v)) <= 600000;
const language = (v: unknown): v is string => typeof v === 'string' && v.length <= 63 && /^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(v);
function invalid(): never { throw new Error(unreadableServerResponse); }
function provenance(v: unknown): LyricProvenance { if (!obj(v) || !['upload', 'sidecar', 'embedded', 'lrclib'].includes(String(v.origin)) || !str(v.providerId, 64) || !str(v.label, 1024) || !str(v.rights, 1024))
    invalid(); return Object.freeze(v) as LyricProvenance; }
function document(v: unknown): LyricDocument {
    if (!obj(v) || !['lrc', 'text'].includes(String(v.format)) || !str(v.text, 262144) || !offset(v.embeddedOffsetMs) || !Array.isArray(v.lines) || !v.lines.length || v.lines.length > 4096)
        invalid();
    let previous = -1, length = 0;
    const format = v.format;
    const lines = v.lines.map((line: unknown) => { if (!obj(line) || !str(line.text, 8192))
        invalid(); if (format === 'lrc' ? !integer(line.atMs) || line.atMs > 86400000 || line.atMs < previous : line.atMs !== null)
        invalid(); if (typeof line.atMs === 'number')
        previous = line.atMs; length += line.text.length; if (length > 262144)
        invalid(); return Object.freeze({ atMs: line.atMs as number | null, text: line.text }); });
    return Object.freeze({ format: format as 'lrc' | 'text', text: v.text, lines: Object.freeze(lines), embeddedOffsetMs: v.embeddedOffsetMs });
}
function revision(v: unknown, content = false): LyricRevision {
    if (!obj(v) || !id(v.id) || !integer(v.revision) || v.revision < 1 || v.revision > 128 || !['private', 'library'].includes(String(v.scope)) || typeof v.canManage !== 'boolean' || typeof v.deleted !== 'boolean' || !language(v.language) || !offset(v.offsetMs) || !['lrc', 'text'].includes(String(v.format)) || !hash(v.digest) || !str(v.createdAt, 64))
        invalid();
    const d = v.document === undefined ? undefined : document(v.document);
    if (content && !d || d && d.format !== v.format || v.format === 'text' && v.offsetMs !== 0)
        invalid();
    return Object.freeze({ ...v, provenance: provenance(v.provenance), ...(d ? { document: d } : {}) }) as LyricRevision;
}
function source(wire: unknown): LyricSource { const v: unknown = wireDurations(wire, {startSeconds: 'startMs', duration: 'durationMs', sourceDuration: 'sourceDurationMs'}); if (!obj(v) || !id(v.id) || !hash(v.version) || !seconds(v.startSeconds) || !seconds(v.duration) || !seconds(v.sourceDuration) || v.duration <= 0 || v.sourceDuration <= 0 || v.startSeconds + v.duration > v.sourceDuration + .1 || typeof v.local !== 'boolean')
    invalid(); return Object.freeze(v) as LyricSource; }
export function parseLyricsView(raw: unknown, scope: ContentScope, target: LyricsTarget): LyricsView {
    if (!obj(raw) || !obj(raw.scope) || raw.scope.serverId !== scope.serverId || raw.scope.itemId !== target.itemId || raw.scope.libraryId !== target.libraryId || !hash(raw.scope.viewerFence) || raw.scope.sessionId !== (target.sessionId ?? '') || raw.scope.sessionGeneration !== (target.sessionGeneration ?? 0) || !Array.isArray(raw.sources) || raw.sources.length > 32 || !Array.isArray(raw.resources) || raw.resources.length > 64 || typeof raw.canShare !== 'boolean' || !obj(raw.selection) || !integer(raw.selection.revision) || !obj(raw.providers) || typeof raw.providers.lrclib !== 'boolean' || typeof raw.providers.configured !== 'boolean' || !integer(raw.providers.revision))
        invalid();
    const sources = raw.sources.map(source), chosen = raw.source === null ? null : source(raw.source), resources = raw.resources.map(v => revision(v)), selected = raw.selection.resource === null ? null : revision(raw.selection.resource, true);
    let librarySelection: LyricsView['librarySelection'];
    if (raw.librarySelection !== undefined) {
        const choice = raw.librarySelection;
        if (!obj(choice) || !integer(choice.revision) || choice.revision < 1) invalid();
        const resource = choice.resource === null ? null : revision(choice.resource, true);
        if (!chosen || resource && (resource.scope !== 'library' || resource.deleted)) invalid();
        librarySelection = Object.freeze({revision: choice.revision, resource});
    }
    if (new Set(sources.map(v => v.id)).size !== sources.length || new Set(resources.map(v => v.id)).size !== resources.length || chosen && (!sources.some(v => v.id === chosen.id && v.version === chosen.version) || target.sourceId && chosen.id !== target.sourceId) || !chosen && (resources.length || selected) || !target.sessionId && selected)
        invalid();
    for (const selectedRevision of [selected, librarySelection?.resource])
        if (selectedRevision?.document && chosen)
            for (const line of selectedRevision.document.lines)
                if (line.atMs !== null && line.atMs > chosen.sourceDuration * 1000 + 1000)
                    invalid();
    return Object.freeze({ ...(librarySelection ? {librarySelection} : {}), scope: Object.freeze({ ...raw.scope }) as LyricsView['scope'], sources: Object.freeze(sources), source: chosen, resources: Object.freeze(resources), selection: Object.freeze({ revision: raw.selection.revision, resource: selected }), canShare: raw.canShare, providers: Object.freeze({ ...raw.providers }) as LyricsView['providers'] });
}
/** Positive offsets delay lyrics. The input is the existing accepted player
 * position, never elapsed wall time or an optimistic pending seek position.
 * Equal timestamps select the last line in document order, including after seeks. */
export function activeLyricLine(doc: LyricDocument, positionSeconds: number, offsetMs: number, sourceStartSeconds = 0): number {
    if (doc.format !== 'lrc' || !Number.isFinite(positionSeconds))
        return -1;
    const at = (positionSeconds + sourceStartSeconds) * 1000 - offsetMs;
    let lo = 0, hi = doc.lines.length;
    while (lo < hi) {
        const mid = (lo + hi) >>> 1;
        if ((doc.lines[mid].atMs ?? Infinity) <= at)
            lo = mid + 1;
        else
            hi = mid;
    }
    return lo - 1;
}
export function lyricSeekPosition(line: LyricLine, offsetMs: number, source: LyricSource): number | null {
    if (line.atMs === null)
        return null;
    const seconds = (line.atMs + offsetMs) / 1000 - source.startSeconds;
    return Number.isFinite(seconds) && seconds >= 0 && seconds < source.duration ? seconds : null;
}
/** One instance belongs to an exact viewer/server/song/playback intent. Async
 * generations fence responses even when a transport ignores AbortSignal. */
export class LyricsService {
    private state: LyricsSnapshot = Object.freeze({ data: null, candidates: [], warnings: [], preview: null, busy: false, error: '', generation: 0 });
    private listeners = new Set<() => void>();
    private controller?: AbortController;
    private generation = 0;
    private disposed = false;
    private fence?: string;
    private choice?: { retire(error: Error): void };
    private readonly api: LyricsApi;
    private readonly scope: ContentScope;
    private readonly target: LyricsTarget;
    constructor(api: LyricsApi, scope: ContentScope, target: LyricsTarget) {
        this.api = api;
        this.scope = Object.freeze({ ...scope });
        this.target = Object.freeze({ ...target });
        if (!id(scope.serverId) || !scope.viewerId || !id(target.itemId) || !id(target.libraryId) || target.sessionId && (!id(target.sessionId) || !integer(target.sessionGeneration) || target.sessionGeneration < 1))
            throw new Error('Lyrics require a bound song and viewer.');
    }
    getSnapshot = () => this.state;
    subscribe = (f: () => void) => { this.listeners.add(f); return () => { this.listeners.delete(f); }; };
    private publish(p: Partial<LyricsSnapshot>) { this.state = Object.freeze({ ...this.state, ...p, generation: this.generation }); for (const f of this.listeners)
        f(); }
    private path(suffix = '') { return encodedRoutePath("get_items_id_lyrics", {"id": encodeURIComponent(this.target.itemId)}) + (suffix); }
    private query() { const q = new URLSearchParams(); if (this.target.sessionId)
        q.set('sessionId', this.target.sessionId); if (this.target.sourceId)
        q.set('sourceId', this.target.sourceId); return q.size ? '?' + q.toString() : ''; }
    private request(path: string, method: string, body: unknown, signal: AbortSignal) { return this.api.requestLyrics ? this.api.requestLyrics<unknown>(path, method, body, signal) : this.api.request<unknown>(path, method, body, signal); }
    private bound() { const source = this.state.data?.source; if (!source)
        throw new Error('No verified song source is selected.'); return { sourceId: source.id, sourceVersion: source.version, sessionId: this.target.sessionId ?? '' }; }
    private check(raw: unknown) { return parseLyricsView(raw, this.scope, this.target); }
    private async run(work: (signal: AbortSignal) => Promise<Partial<LyricsSnapshot>>) {
        if (this.disposed)
            return;
        const previousGeneration = this.generation;
        this.choice?.retire(new Error('Lyrics choice was replaced by another service operation.'));
        if (this.disposed || this.generation !== previousGeneration || this.choice) return;
        const g = ++this.generation;
        const previous = this.controller; this.controller = undefined; previous?.abort();
        if (this.disposed || g !== this.generation) return;
        const controller = new AbortController();
        this.controller = controller;
        this.publish({ busy: true, error: '' });
        if (this.disposed || g !== this.generation || this.controller !== controller) return;
        let timer: ReturnType<typeof setTimeout> | undefined;
        try {
            let result = await Promise.race([work(controller.signal), new Promise<never>((_, reject) => { timer = setTimeout(() => { controller.abort(); reject(new Error('Lyrics request timed out.')); }, 22000); })]);
            if (g === this.generation && !this.disposed) {
                if (result.data) {
                    const incoming = result.data;
                    if (this.fence && this.fence !== incoming.scope.viewerFence) {
                        const error = new Error('Viewer permissions changed.') as Error & {
                            status: number;
                        };
                        error.status = 401;
                        throw error;
                    }
                    this.fence = incoming.scope.viewerFence;
                    const oldPreview = this.state.preview;
                    if (oldPreview && !incoming.resources.some(r => r.id === oldPreview.id && r.revision === oldPreview.revision))
                        result = { ...result, preview: null };
                    if (this.state.data?.source?.version !== incoming.source?.version) {
                        result = { ...result, preview: null, candidates: [], warnings: [] };
                    }
                }
                this.publish({ ...result, busy: false });
            }
        }
        catch (error) {
            if (g !== this.generation || this.disposed)
                return;
            const e = error as {
                code?: string;
                status?: number;
            };
            const denied = e.status === 401 || e.status === 403, changed = e.code === 'lyrics_source_changed';
            if (denied)
                this.fence = undefined;
            this.publish({ busy: false, ...(denied || changed ? { data: null, preview: null, candidates: [] } : {}), error: denied ? 'This viewer can no longer access these lyrics.' : changed ? 'The song source changed. Start a new playback session or reopen the song.' : e.code === 'lyrics_conflict' || e.status === 409 ? 'Lyrics changed. Refresh and review the latest revision before trying again.' : e.code === 'invalid_lyrics' ? 'Invalid lyrics. Check text encoding, timestamps, language and offset.' : e.code === 'lyrics_capacity' || e.status === 429 ? 'A lyric resource, revision, or request limit was reached. For request limits, wait before retrying.' : e.code === 'lyrics_unavailable' ? 'Lyrics could not be acquired. Check source access or provider configuration.' : 'Lyrics could not be loaded or saved. Refresh before retrying.' });
        }
        finally {
            if (timer)
                clearTimeout(timer);
        }
    }
    // A song with no lyrics answers 404: that is the empty state ("No lyrics"), not a failure.
    refresh = () => this.run(async (signal) => { let raw: unknown; try { raw = await this.request(this.path() + this.query(), 'GET', undefined, signal); } catch (e) { if ((e as { status?: number } | null)?.status === 404) return { data: null, preview: null, candidates: [] }; throw e; } return { data: this.check(raw) }; });
    choose = (resource: LyricRevision | null, context?: LyricsChooseContext): Promise<void> => { if (context !== undefined) return this.chooseScoped(resource, context); const d = this.state.data; if (!d || !this.target.sessionId)
        return Promise.resolve(); const body = { ...this.bound(), expectedSelectionRevision: d.selection.revision, resourceId: resource?.id ?? '', revision: resource?.revision ?? 0 }; return this.mutate('/selection', 'PUT', body); };
    private chooseScoped(resource: LyricRevision | null, context: LyricsChooseContext): Promise<void> {
        const generation = this.generation, shown = context.snapshot, signal = context.signal, current = context.isCurrent;
        const refused = () => new Error('Lyrics choice belongs to a retired or unavailable command.');
        const admitted = () => !this.disposed && this.generation === generation && !this.choice && this.state === shown && !shown.busy;
        if (!admitted() || signal.aborted) return Promise.reject(refused());
        let allowed = false;
        try { allowed = current.call(context) === true; } catch {}
        if (!allowed || signal.aborted || !admitted()) return Promise.reject(refused());
        const data = shown.data, source = data?.source;
        if (!data || !source || !this.target.sessionId || !integer(this.target.sessionGeneration) || this.target.sessionGeneration < 1
            || shown.generation !== generation || data.scope.sessionId !== this.target.sessionId || data.scope.sessionGeneration !== this.target.sessionGeneration
            || data.scope.serverId !== this.scope.serverId || data.scope.libraryId !== this.target.libraryId || data.scope.itemId !== this.target.itemId
            || this.target.sourceId && source.id !== this.target.sourceId || data.librarySelection !== undefined
            || data.selection.revision >= Number.MAX_SAFE_INTEGER || resource !== null && (!data.resources.includes(resource) || resource.deleted)) return Promise.reject(refused());
        const originalSource = Object.freeze({ ...source });
        const requested = resource === null ? null : Object.freeze({ id: resource.id, revision: resource.revision, digest: resource.digest, scope: resource.scope });
        const body = Object.freeze({ sourceId: originalSource.id, sourceVersion: originalSource.version, sessionId: this.target.sessionId,
            expectedSelectionRevision: data.selection.revision, resourceId: requested?.id ?? '', revision: requested?.revision ?? 0 });
        const putPath = this.path('/selection'), getPath = this.path() + this.query(), scope = this.scope, target = this.target;
        // Capture the original method and receiver once, including getter reentry.
        const transport = (this.api.requestLyrics ?? this.api.request).bind(this.api);
        if (!admitted() || signal.aborted) return Promise.reject(refused());
        let expected = shown, done = false, timer: ReturnType<typeof setTimeout> | undefined;
        const controller = new AbortController();
        let resolve!: () => void, reject!: (error: unknown) => void;
        const completion = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
        const owns = () => !this.disposed && this.choice === operation && this.generation === generation && this.state === expected;
        const clean = () => { if (timer !== undefined) clearTimeout(timer); timer = undefined; signal.removeEventListener('abort', abort); };
        const stop = (error: Error, patch?: Partial<LyricsSnapshot>) => {
            if (done) return;
            done = true; const owned = owns();
            if (this.choice === operation) this.choice = undefined;
            clean(); reject(error);
            // Restore only our still-visible busy bit before any subscriber/HTTP abort reentry.
            try { if (owned && this.state.busy) this.publish({ busy: false, ...patch }); }
            finally { controller.abort(); }
        };
        const operation = { retire: stop };
        const abort = () => stop(refused());
        const check = () => {
            if (done) return false;
            if (!owns() || signal.aborted) { stop(refused()); return false; }
            let live = false; try { live = current.call(context) === true; } catch {}
            if (!live || signal.aborted || !owns() || done) { stop(refused()); return false; }
            return true;
        };
        this.choice = operation;
        signal.addEventListener('abort', abort, { once: true });
        if (!check()) return completion;
        timer = setTimeout(() => stop(new Error('Lyrics request timed out.')), 22000);
        // Reserve the completion and token before notifying synchronous subscribers.
        expected = Object.freeze({ ...shown, busy: true, error: '', generation });
        this.state = expected;
        try { for (const listener of this.listeners) listener(); } catch (error) { stop(error instanceof Error ? error : refused()); }
        if (!check()) return completion;
        const sameResource = (actual: LyricRevision | null) => requested === null ? actual === null
            : actual !== null && !actual.deleted && actual.id === requested.id && actual.revision === requested.revision && actual.digest === requested.digest && actual.scope === requested.scope;
        const work = async () => {
            if (!check()) return;
            const receipt = await transport<unknown>(putPath, 'PUT', body, controller.signal);
            if (!check()) return;
            if (!obj(receipt) || !integer(receipt.revision) || receipt.revision !== body.expectedSelectionRevision + 1) invalid();
            const selected = receipt.resource === null ? null : revision(receipt.resource, true);
            if (!sameResource(selected)) invalid();
            if (!check()) return;
            const raw = await transport<unknown>(getPath, 'GET', undefined, controller.signal);
            if (!check()) return;
            const incoming = parseLyricsView(raw, scope, target);
            if (incoming.scope.viewerFence !== data.scope.viewerFence || incoming.source?.id !== originalSource.id || incoming.source.version !== originalSource.version
                || incoming.librarySelection !== undefined || incoming.selection.revision !== receipt.revision || !sameResource(incoming.selection.resource)) invalid();
            if (!check()) return;
            done = true; this.choice = undefined; clean();
            this.fence = incoming.scope.viewerFence;
            try { this.publish({ data: incoming, candidates: [], preview: null, busy: false, error: '' }); }
            finally { resolve(); }
        };
        // Ignored-abort transport work remains observed and can never continue after retirement.
        void work().catch(error => {
            if (!check()) return;
            const e = error as { status?: number; code?: string } | null;
            const denied = e?.status === 401 || e?.status === 403, changed = e?.code === 'lyrics_source_changed';
            if (denied) this.fence = undefined;
            stop(error instanceof Error ? error : refused(), denied || changed ? {
                data: null, preview: null, candidates: [], warnings: [],
                error: denied ? 'This viewer can no longer access these lyrics.' : 'The song source changed. Start a new playback session or reopen the song.',
            } : undefined);
        });
        return completion;
    }
    private mutate(suffix: string, method: string, body: unknown) { return this.run(async (signal) => { await this.request(this.path(suffix), method, body, signal); const raw = await this.request(this.path() + this.query(), 'GET', undefined, signal); return { data: this.check(raw), candidates: [], preview: null }; }); }
    upload = (input: LyricUpload) => !this.state.data?.source ? Promise.resolve() : this.mutate('', 'POST', { ...this.bound(), ...input, resourceId: input.resourceId ?? '', expectedRevision: input.expectedRevision ?? 0 });
    setOffset = (resource: LyricRevision, milliseconds: number) => !this.state.data?.source ? Promise.resolve() : this.mutate('/' + encodeURIComponent(resource.id) + '/offset', 'PATCH', { ...this.bound(), expectedRevision: resource.revision, offsetMs: milliseconds });
    remove = (resource: LyricRevision) => !this.state.data?.source ? Promise.resolve() : this.mutate('/' + encodeURIComponent(resource.id), 'DELETE', { ...this.bound(), expectedRevision: resource.revision });
    search = (provider: 'local' | 'lrclib', query: string, language: string, replacement?: LyricRevision) => {
        if (!this.state.data?.source)
            return Promise.resolve();
        this.publish({ candidates: [], warnings: [] });
        const body = { ...this.bound(), provider, query, language, resourceId: replacement?.id ?? '', expectedRevision: replacement?.revision ?? 0 };
        return this.run(async (signal) => { const raw = await this.request(this.path('/search'), 'POST', body, signal); if (!obj(raw) || !Array.isArray(raw.candidates) || raw.candidates.length > 24 || !Array.isArray(raw.warnings) || raw.warnings.length > 12 || !raw.warnings.every(v => str(v, 512)))
            invalid(); // A candidate this app cannot read (a format or source newer than it) is left out; the rest are offered.
            const candidates = keepReadable('lyrics', 'candidates', raw.candidates, (v: unknown) => { if (!obj(v) || !id(v.id) || !languageTag(v.language) || !['text', 'lrc'].includes(String(v.format)) || !str(v.preview, 512))
            invalid(); return Object.freeze({ ...v, provenance: provenance(v.provenance) }) as LyricCandidate; }); return { candidates: Object.freeze(candidates), warnings: Object.freeze(raw.warnings as string[]) }; });
    };
    preview = (resource: LyricRevision) => this.run(async (signal) => { const raw = await this.request(this.path('/' + encodeURIComponent(resource.id) + '/revisions/' + resource.revision) + this.query(), 'GET', undefined, signal); const r = revision(raw, true); if (r.id !== resource.id || r.revision !== resource.revision)
        invalid(); return { preview: r }; });
    configureProvider = (enabled: boolean) => { const revision = this.state.data?.providers.revision; if (revision === undefined)
        return Promise.resolve(); return this.run(async (signal) => { await this.request(routePath("put_lyrics_provider"), 'PUT', routeBody("put_lyrics_provider", { enabled, expectedRevision: revision }), signal); const raw = await this.request(this.path() + this.query(), 'GET', undefined, signal); return { data: this.check(raw), candidates: [] }; }); };
    cancel = () => {
        const generation = this.generation; this.choice?.retire(new Error('Lyrics choice was cancelled.'));
        if (this.disposed || generation !== this.generation || this.choice) return;
        this.generation++; const controller = this.controller; this.controller = undefined; this.fence = undefined;
        try { this.publish({ data: null, preview: null, candidates: [], warnings: [], busy: false, error: '' }); } finally { controller?.abort(); }
    };
    dispose = () => { this.disposed = true; this.generation++; const controller = this.controller; this.controller = undefined; this.listeners.clear(); this.choice?.retire(new Error('Lyrics service was disposed.')); controller?.abort(); };
}
const languageTag = language;
