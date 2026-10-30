import {useLayoutEffect, useState} from 'react';
import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import {parsePreferenceSnapshot, type ReadingPreferences} from '@portico/client-core';
import {createReadingController, ReaderError, sourceFromBlob, type ReaderAdapter, type ReaderMount, type ReaderPageImage, type ReadingPublication} from '@portico/reader-core';
import bootstrap from '../../../server/testdata/contracts/get-bootstrap.json';
import {ReaderBook, type ReaderBookState} from '../../src/screens/read/ReaderBook';
import '../../src/kit/tokens.css';
import '../../src/bootstrap.css';
import fixedStyles from '../../src/screens/read/fixed-page.module.css';

const png = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jC1sAAAAASUVORK5CYII='), c => c.charCodeAt(0));
const image = (): ReaderPageImage => ({mime: 'image/png', bytes: png, width: 1, height: 1});
const preferences = parsePreferenceSnapshot(bootstrap.response.preferences);
type Pending = {resolve: (image: ReaderPageImage) => void; reject: (error: unknown) => void};
type Read = {source: string; index: number; width: number; signal: AbortSignal};
type Harness = {rerender(): void; advance(page: number): Promise<void>; defer(index: number): void; reject(): Promise<void>; resolve(): void; replace(): void; retire(): void; observations(): {reads: {source: string; index: number; width: number; aborted: boolean}[]; created: string[]; revoked: string[]; displayed: string[]; source: string; errors: {source: string; error: unknown}[]; listeners: number};};
declare global {interface Window {fixedPreviewLifecycle?: Harness; disposeFixedPreviewLifecycle?: () => void;}}

/** Local test ports only. ReaderSurface opens and closes each actual controller. */
export function mountFixedPreviewLifecycle() {
  const host = document.createElement('div'); host.dataset.fixedPreviewFixture = ''; host.style.cssText = 'width:900px;height:700px;position:fixed;inset:0'; document.body.append(host);
  const originalCreate = URL.createObjectURL, originalRevoke = URL.revokeObjectURL;
  const created: string[] = [], revoked: string[] = [], reads: Read[] = [], errors: {source: string; error: unknown}[] = [], owned = new Set<string>();
  URL.createObjectURL = function(blob) {const url = originalCreate.call(URL, blob); created.push(url); return url;};
  URL.revokeObjectURL = function(url) {revoked.push(url); originalRevoke.call(URL, url);};
  let pending: Pending | undefined, deferredIndex: number | undefined, serial = 0;
  const sources: ReturnType<typeof makeBook>[] = [];
  function makeBook() {
    const id = `fixed-preview-fixture-${++serial}`;
    const source = sourceFromBlob(new Blob([new Uint8Array([0])]), id, 'sha256:6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d');
    const abort = new AbortController(), listeners = new Set<() => void>(); let live = true, mount: ReaderMount<ReadingPreferences> | undefined;
    const authority = {isCurrent: () => live, subscribe(fn: () => void) {listeners.add(fn); return () => {listeners.delete(fn);};}};
    const adapter: ReaderAdapter<ReadingPreferences> = {format: 'pdf', async open({source: opened}) {return {
      id: opened.id, revision: opened.revision, format: 'pdf', metadata: {pageCount: 40}, toc: [],
      async mount(options) {mount = options; return {async navigate(target) {if (target.kind === 'location') options.onLocation(target.location);}, async resize() {}, async applyPreferences() {}, getLocation: () => null, dispose() {}};},
      async *search() {}, async page(index, options) {
        reads.push({source: id, index, width: options.width, signal: options.signal});
        if (index === deferredIndex) {deferredIndex = undefined; return new Promise<ReaderPageImage>((resolve, reject) => {pending = {resolve, reject};});}
        return image();
      }, dispose() {},
    };}};
    const controller = createReadingController({adapters: [adapter], preferenceSnapshot: preferences});
    const publication: ReadingPublication = {format: 'pdf', source, metadata: {title: id}, signal: abort.signal,
      resumeLocation: () => null, contentsTarget: () => {throw new Error('no contents');}, dispose() {abort.abort();}};
    const book: ReaderBookState = {publication, controller, authority, location: {format: 'pdf', page: 10, progression: 10 / 39}};
    return {id, book, controller, listeners, reportError: (error: unknown) => {errors.push({source: id, error});}, retire() {live = false; abort.abort(); for (const fn of [...listeners]) fn();}, get mount() {return mount;}};
  }
  const first = makeBook(); sources.push(first);
  const root = createRoot(host);
  function Fixture() {
    const [current, setCurrent] = useState(first), [, setVersion] = useState(0);
    useLayoutEffect(() => {
      window.fixedPreviewLifecycle = {
        rerender: () => flushSync(() => setVersion(value => value + 1)),
        advance: async page => {await current.controller.navigate({kind: 'location', location: {format: 'pdf', page, progression: page / 39}});},
        defer: index => {deferredIndex = index;}, reject: async () => {const held = pending; if (!held) throw new Error('no pending preview'); pending = undefined; held.reject(new ReaderError('invalid_book')); await new Promise<void>(resolve => setTimeout(resolve, 0));},
        resolve: () => {const held = pending; pending = undefined; held?.resolve(image());},
        replace: () => {current.retire(); const next = makeBook(); sources.push(next); flushSync(() => setCurrent(next));},
        retire: () => current.retire(),
        observations: () => {
          const displayed = [...host.querySelectorAll<HTMLImageElement>(`.${fixedStyles.strip} img`)].map(img => img.src);
          for (const url of displayed) if (created.includes(url)) owned.add(url);
          return {reads: reads.map(r => ({source: r.source, index: r.index, width: r.width, aborted: r.signal.aborted})), created: created.filter(url => owned.has(url)), revoked: revoked.filter(url => owned.has(url)), displayed, source: current.id, errors: [...errors], listeners: sources.reduce((sum, source) => sum + source.listeners.size, 0)};
        },
      };
    });
    return <ReaderBook book={current.book} active reportError={current.reportError} onBack={() => {}} onOpenSettings={() => {}}/>;
  }
  flushSync(() => root.render(<Fixture/>));
  return () => {for (const source of sources) source.retire(); flushSync(() => root.unmount()); host.remove(); URL.createObjectURL = originalCreate; URL.revokeObjectURL = originalRevoke; delete window.fixedPreviewLifecycle;};
}
