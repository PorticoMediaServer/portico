import React, {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Image, ScrollView, Text, View} from 'react-native';
import {MetadataRepairService, type RepairData} from '../../../../../packages/client-core/src/metadata-repair';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable, TextField} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {overlayPaintPermit, RetainedOverlay} from '../../kit/overlays/SurfacePaintHost';
import {LoadingBoundary, SkeletonBlock, StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {stageCoverImage, type CoverImageMime, type CoverImageTemp} from '../../platform/cover-image';
import {rereadNativeReadingSource} from './read-reread';
import {loadNativeCoverPages, type NativeCoverPage} from './read-cover-pages';
import type {ReadingManifest, ReadingPage} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeReadOwnerEditLease, NativeReadVersion} from './read-book-model';

const {t} = defaultI18n;
const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
const sourceLabel = (source: string | undefined): string | undefined =>
  !source || source === 'automatic' ? undefined
    : source === 'file-metadata' ? t('next.reader.sourceFileMetadata')
      : source === 'file-name' ? t('next.reader.sourceFileName')
        : source === 'folder' ? t('next.reader.sourceFolder')
          : source === 'owner' || source === 'manual' ? t('next.reader.sourceOwner')
            : t('web.metadata.fromSource', {source});

/** The immutable cover opening: the exact repair document and selected manifest
 * captured when the drawn control opened the nested sheet. A later repair
 * document or page render never becomes this opening's subject. */
type CoverOpening = Readonly<{
  key: number; data: RepairData; manifest: ReadingManifest; itemId: string; sourceId: string;
  revision: string; pageCount: number; pageNames: readonly string[];
}>;

/** phone-read 3b: the owner's Reading edit sheet over the single-item repair
 * producer. It is bound to the committed book page it was opened from: every
 * request is scoped to that route/revision, session family and authority, and
 * the sheet retires with its page or its own close. */
export function ReadOwnerEditSheet({context, lease, owner, itemId, libraryId, manifest, version, onClose}: Readonly<{
  context: NativeShellBootstrapContext; lease: NativeReadOwnerEditLease; owner: object;
  itemId: string; libraryId: string; manifest?: ReadingManifest; version?: NativeReadVersion; onClose: (owner: object) => void;
}>) {
  const theme = useTheme();
  const sheetLife = useRef<AbortController>(new AbortController()).current;
  const linked = useMemo(() => linkNativeAbortSignals([lease.signal, sheetLife.signal]), [lease, sheetLife]);
  const owned = useMemo(() => () => !linked.signal.aborted && lease.isCurrent(), [lease, linked]);
  // One render-owned ticket serializes editing, artwork selection and the CBZ
  // cover operation. The nested cover opening owns a separate flight so its
  // close can abort staging and upload without retiring the outer sheet.
  const operation = useRef<object | undefined>(undefined);
  const coverCurrent = useRef<CoverOpening | null>(null);
  const coverFlight = useRef<Readonly<{opening: CoverOpening; controller: AbortController}> | undefined>(undefined);
  // The page-preview strip is presentation-only and tied to its exact opening.
  const pagesFlight = useRef<AbortController | undefined>(undefined);
  const coverSeq = useRef<number>(0);
  // The page-number draft is opaque and revisioned: a retained Choose callback
  // can only submit the exact draft it was drawn with.
  const pageCurrent = useRef<Readonly<{revision: number; value: string}>>(Object.freeze({revision: 0, value: ''}));
  const api = useMemo(() => {
    // A transport may resolve after its scoped signal was retired; the result is
    // never admitted, and the repair service keeps the failure as uncertain.
    const admitted = (scoped: AbortSignal, flight: AbortController | undefined, ticket: object | undefined) => {
      if (!owned() || scoped.aborted) throw cancelled();
      if (flight && (coverFlight.current?.controller !== flight || operation.current !== ticket)) throw cancelled();
    };
    return {
      request: async <T,>(path: string, method?: string, body?: unknown, callerSignal?: AbortSignal): Promise<T> => {
        const flight = coverFlight.current?.controller, ticket = operation.current;
        const scoped = linkNativeAbortSignals([linked.signal, callerSignal, flight?.signal]);
        try {
          admitted(scoped.signal, flight, ticket);
          const result = await context.api.request<T>(path, method, body, scoped.signal);
          admitted(scoped.signal, flight, ticket);
          return result;
        } finally {scoped.dispose();}
      },
      requestMetadataUpload: async <T,>(path: string, body: FormData, callerSignal?: AbortSignal): Promise<T> => {
        const flight = coverFlight.current?.controller, ticket = operation.current;
        const scoped = linkNativeAbortSignals([linked.signal, callerSignal, flight?.signal]);
        try {
          admitted(scoped.signal, flight, ticket);
          const result = await context.api.requestMetadataUpload<T>(path, body, scoped.signal);
          admitted(scoped.signal, flight, ticket);
          return result;
        } finally {scoped.dispose();}
      },
    };
  }, [context, linked, owned]);
  const service = useMemo(() => new MetadataRepairService({api,
    scope: {serverId: lease.viewer.serverId, viewerId: JSON.stringify([lease.viewer.authority, lease.viewer.accountId, lease.viewer.profileId])}}),
  [api, lease.viewer.serverId, lease.viewer.authority, lease.viewer.accountId, lease.viewer.profileId]);
  const [snapshot, setSnapshot] = useState(service.getSnapshot());
  const [draft, setDraft] = useState<Readonly<Record<string, string>>>({});
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const [coverOpening, setCoverOpening] = useState<CoverOpening | null>(null);
  const [coverPages, setCoverPages] = useState<Readonly<{opening: CoverOpening; phase: 'loading' | 'ready' | 'failed'; pages: readonly NativeCoverPage[]}>>();
  const [pageInput, setPageInput] = useState(pageCurrent.current);
  const [coverBusy, setCoverBusy] = useState(false), [coverFailed, setCoverFailed] = useState(false);
  const [rereadBusy, setRereadBusy] = useState(false), [rereadFailed, setRereadFailed] = useState(false);
  const client = useMemo(() => context.api.reading({viewer: lease.viewer, isCurrent: () => owned(), signal: linked.signal}), [context, lease.viewer, linked, owned]);
  const loaded = useRef<RepairData | undefined>(undefined), retired = useRef<boolean>(false);
  // Each opening is its own keyed instance: its owner ticket and close callback
  // are captured once and never follow a successor.
  const opening = useRef<Readonly<{owner: object; onClose: (owner: object) => void}> | undefined>(undefined);
  if (!opening.current) opening.current = Object.freeze({owner, onClose});
  const close = () => {if (!retired.current) {retired.current = true; sheetLife.abort(); pagesFlight.current?.abort(); coverFlight.current?.controller.abort(); opening.current!.onClose(opening.current!.owner);}};
  useLayoutEffect(() => {
    if (!owned()) {close(); return;}
    // The original page leaving its route or authority retires this sheet and
    // its in-flight repair immediately; Back never revives it.
    const retire = () => {if (!retired.current && !lease.isCurrent()) close();};
    const offNav = context.navigation.subscribe(retire), offAuthority = context.subscribeAuthority(retire), offModel = lease.subscribe(retire);
    const off = service.subscribe(() => {
      const next = service.getSnapshot();
      setSnapshot(next);
      // Only an owned successful load resets the draft; saving/conflict events
      // never clear what the owner is typing.
      if (next.phase === 'ready' && next.data && next.data !== loaded.current && owned() && !retired.current) {
        loaded.current = next.data; setDraft({});
      }
    });
    void service.load({kind: 'item', id: itemId, libraryId});
    return () => {offNav(); offAuthority(); offModel(); off(); retired.current = true; sheetLife.abort(); pagesFlight.current?.abort(); coverFlight.current?.controller.abort(); linked.dispose(); client.dispose(); service.dispose();};
  }, [context, service, itemId, libraryId, owned, sheetLife, linked, lease]); // eslint-disable-line react-hooks/exhaustive-deps
  const data: RepairData | null | undefined = snapshot.data;
  const exact = (value: RepairData | null | undefined): value is RepairData => !!value && owned()
    && lease.origin.route.kind === 'read-book' && lease.origin.route.libraryId === libraryId && lease.origin.route.item.id === itemId
    && value.target.kind === 'item' && value.target.id === itemId && value.libraryId === libraryId
    && value.serverId === lease.viewer.serverId;
  /** The exact selected manifest the sheet was opened for: source revision and
   * page bounds are witnessed, never inferred from a page count. */
  const coverWitness = (value: RepairData | null | undefined, source: ReadingManifest | undefined) => {
    if (!exact(value) || !source || source.itemId !== itemId || source.format !== 'cbz' || !source.sourceId
      || typeof source.revision !== 'string' || !source.revision || !source.pageNames?.length) return undefined;
    return Object.freeze({data: value, manifest: source, itemId: source.itemId, sourceId: source.sourceId,
      revision: source.revision, pageCount: source.pageNames.length, pageNames: source.pageNames});
  };
  const save = async () => {
    if (!exact(data) || operation.current) return;
    const fields: Record<string, {value: string}> = {};
    for (const spec of data.schema) {
      const current = data.snapshot.fields[spec.field];
      if (!current) continue;
      const next = draft[spec.field];
      if (next !== undefined && next !== current.value) fields[spec.field] = {value: next};
    }
    if (!Object.keys(fields).length) {if (owned()) close(); return;}
    const capturedData = data, ticket = {};
    operation.current = ticket;
    setBusy(true); setFailed(false);
    try {
      const saved = await service.command({action: 'edit', fields}, capturedData, {signal: linked.signal, current: owned});
      if (!owned() || operation.current !== ticket) return;
      if (saved) close(); else setFailed(true);
    } catch {if (owned() && operation.current === ticket) setFailed(true);}
    finally {if (operation.current === ticket) {operation.current = undefined; if (owned()) setBusy(false);}}
  };
  const selectCover = async (candidateId: string) => {
    if (!exact(data) || operation.current) return;
    const capturedData = data;
    const candidate = capturedData.artwork.candidates.find(value => value.id === candidateId && value.role === 'cover');
    if (!candidate || candidate.current) return;
    const ticket = {};
    operation.current = ticket;
    setCoverBusy(true); setCoverFailed(false);
    try {
      const selected = await service.command({action: 'select_artwork', candidateId}, capturedData, {signal: linked.signal, current: owned});
      if (!owned() || operation.current !== ticket) return;
      // A false command is a refused selection, never a silent success.
      if (!selected) setCoverFailed(true);
    } catch {if (owned() && operation.current === ticket) setCoverFailed(true);}
    finally {if (operation.current === ticket) {operation.current = undefined; if (owned()) setCoverBusy(false);}}
  };
  const setPage = (value: string) => {
    const next = Object.freeze({revision: pageCurrent.current.revision + 1, value});
    pageCurrent.current = next; setPageInput(next);
  };
  /** Bounded real page previews for this exact opening: same source, revision,
   * page bounds and sheet lifetime; nothing is relooked up. */
  const loadCoverPages = async (target: CoverOpening) => {
    if (coverCurrent.current !== target) return;
    const flight = new AbortController();
    pagesFlight.current = flight;
    const scoped = linkNativeAbortSignals([linked.signal, flight.signal]);
    setCoverPages({opening: target, phase: 'loading', pages: []});
    try {
      const pages = await loadNativeCoverPages({client, signal: scoped.signal, opening: {itemId: target.itemId, sourceId: target.sourceId,
        revision: target.revision, pageCount: target.pageCount, pageNames: target.pageNames},
        isCurrent: () => coverCurrent.current === target && owned() && !scoped.signal.aborted});
      if (coverCurrent.current !== target || !owned()) return;
      setCoverPages({opening: target, phase: 'ready', pages});
    } catch {if (coverCurrent.current === target && !scoped.signal.aborted) setCoverPages({opening: target, phase: 'failed', pages: []});}
    finally {scoped.dispose(); if (pagesFlight.current === flight) pagesFlight.current = undefined;}
  };
  /** The drawn control captures the immutable opening now; later renders never
   * substitute their own repair document or manifest for it. */
  const openCover = () => {
    const live = service.getSnapshot();
    // A retained control may not open a repair document the sheet no longer holds.
    if (!exact(data) || live.phase !== 'ready' || live.data !== data || operation.current || coverCurrent.current) return;
    const witness = coverWitness(data, manifest);
    if (!witness) return;
    const next = Object.freeze({...witness, key: ++coverSeq.current});
    coverCurrent.current = next;
    setPage(''); setCoverFailed(false); setCoverPages(undefined); setCoverOpening(next);
    void loadCoverPages(next);
  };
  /** Ask the server to re-read the exact original source and refresh its file
   * facts. The rendered repair document, manifest and selected version are
   * captured together; the pre-I/O witness and every post-await lifetime check
   * keep a retired sheet from refreshing or publishing a successor's state. */
  const reread = async () => {
    if (!exact(data) || !manifest || !version || operation.current) return;
    const live = service.getSnapshot();
    if (live.phase !== 'ready' || live.data !== data) return;
    const capturedData = data, capturedManifest = manifest, capturedVersion = version, ticket = {};
    operation.current = ticket;
    setRereadBusy(true); setRereadFailed(false);
    try {
      await rereadNativeReadingSource({api: context.api, itemId, manifest: capturedManifest, version: capturedVersion,
        data: capturedData, signal: linked.signal, isCurrent: owned});
      if (!owned() || operation.current !== ticket) return;
      // Accepted values come from the repair document, not the receipt's field names.
      await service.refresh(capturedData);
      if (!owned() || operation.current !== ticket) return;
    } catch {if (owned() && operation.current === ticket) setRereadFailed(true);}
    finally {if (operation.current === ticket) {operation.current = undefined; if (owned()) setRereadBusy(false);}}
  };
  /** Closing the nested sheet retires only its own opening and flight; a
   * retained close from a superseded opening is refused. */
  const dismissCover = (target: CoverOpening) => {
    if (coverCurrent.current !== target) return;
    coverCurrent.current = null;
    pagesFlight.current?.abort(); pagesFlight.current = undefined;
    const flight = coverFlight.current;
    if (flight?.opening === target) {coverFlight.current = undefined; flight.controller.abort();}
    setCoverOpening(null); setPage(''); setCoverFailed(false); setCoverPages(undefined);
  };
  const choosePage = async (target: CoverOpening, input: Readonly<{revision: number; value: string}>) => {
    if (coverCurrent.current !== target || operation.current || !owned() || pageCurrent.current !== input) return;
    // The opening's own ready document is required before any preview session
    // is opened; a retained opening is closed rather than served.
    const entry = service.getSnapshot();
    if (entry.phase !== 'ready' || entry.data !== target.data) {dismissCover(target); return;}
    // A page choice ends the preview flight before its own session opens.
    pagesFlight.current?.abort(); pagesFlight.current = undefined;
    const index = Number(input.value.trim()) - 1;
    const pageName = Number.isInteger(index) && index >= 0 && index < target.pageCount ? target.pageNames[index] : undefined;
    if (pageName === undefined) return;
    const ticket = {}, flight = new AbortController();
    operation.current = ticket;
    coverFlight.current = Object.freeze({opening: target, controller: flight});
    const scoped = linkNativeAbortSignals([linked.signal, flight.signal]);
    const alive = () => owned() && coverCurrent.current === target && operation.current === ticket
      && coverFlight.current?.controller === flight && !scoped.signal.aborted;
    // This opening's own repair document is the only one a cover may use.
    const sameDocument = () => service.getSnapshot().data === target.data;
    setCoverBusy(true); setCoverFailed(false);
    let temp: CoverImageTemp | undefined;
    try {
      const session = await client.open({itemId, sourceId: target.sourceId}, scoped.signal);
      let image: ReadingPage | undefined, closeError: unknown;
      try {
        if (!alive() || !sameDocument()) throw cancelled();
        const actual = session.manifest, pageNames = actual.pageNames;
        if (actual.itemId !== target.itemId || actual.sourceId !== target.sourceId || actual.format !== 'cbz'
          || actual.revision !== target.revision || !pageNames || pageNames.length !== target.pageCount
          || pageNames[index] !== pageName) throw new Error('invalid_cover_source');
        image = await session.page(index, {width: 1280, signal: scoped.signal});
      } finally {try {await session.close();} catch (error) {closeError = error;}}
      if (closeError) throw closeError;
      if (!alive()) return;
      if (!image || image.index !== index || image.name !== pageName) throw new Error('invalid_cover_page');
      if (!sameDocument()) throw new Error('cover_document_changed');
      if (image.mime !== 'image/jpeg' && image.mime !== 'image/png') throw new Error('unsupported_cover');
      temp = await stageCoverImage(image.bytes, image.mime as CoverImageMime, {signal: scoped.signal, isCurrent: alive});
      if (!temp || !alive()) return;
      // The upload must still belong to this opening's own document.
      const live = service.getSnapshot();
      if (live.phase !== 'ready' || live.data !== target.data) throw new Error('cover_document_changed');
      const part = {uri: temp.uri, name: image.mime === 'image/jpeg' ? 'cover.jpg' : 'cover.png', type: image.mime, size: temp.size} as unknown as Blob;
      const uploaded = await service.uploadArtwork('cover', part, '');
      if (!alive()) return;
      if (!uploaded) throw new Error('cover_upload_failed');
      coverCurrent.current = null;
      setCoverOpening(null); setPage(''); setCoverPages(undefined);
    } catch {if (alive()) setCoverFailed(true);}
    finally {
      await temp?.dispose();
      scoped.dispose();
      if (coverFlight.current?.controller === flight) coverFlight.current = undefined;
      if (operation.current === ticket) {operation.current = undefined; if (owned()) setCoverBusy(false);}
    }
  };
  const paintPermit = overlayPaintPermit(() => !lease.signal.aborted && lease.isCurrent(), [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener), listener => lease.subscribe(listener)], [lease.signal]);
  const paintTypes = [BottomSheet, React.Fragment, View, Text, ScrollView, Image, Button, Focusable, TextField, LoadingBoundary, SkeletonBlock, StateView];
  return <RetainedOverlay permit={paintPermit} allowedTypes={paintTypes}><BottomSheet open onOpenChange={open => {if (!open) close();}} title={t('next.reader.editDetails')}
    description={t('next.reader.ownerOnly')} testID="native-read-owner-edit" keyboardAvoiding
    footer={<View style={{flexDirection: 'row', justifyContent: 'flex-end', gap: 10}}>
      <Button variant="glass" onPress={() => {if (owned()) close();}} disabled={busy}>{t('action.cancel')}</Button>
      <Button onPress={() => {void save();}} loading={busy} disabled={!exact(data) || busy || coverBusy || rereadBusy}>{t('action.save')}</Button>
    </View>}>
    {snapshot.phase === 'error' || failed ? <StateView variant="inline" title={t('error.unknown')}/> : null}
    <LoadingBoundary ready={exact(data) || snapshot.phase !== 'idle' && snapshot.phase !== 'loading'} immediate={exact(data)} skeleton={<SkeletonBlock width="100%" height={220}/>}>
    {exact(data) ? <View style={{gap: 12}}>
      {data.schema.map(spec => {
        const field = data.snapshot.fields[spec.field];
        if (!field) return null;
        const provenance = sourceLabel(field.source);
        return <View key={spec.field} style={{gap: 4}}>
          <Text style={[browseType(theme, 13, 700), {color: theme.colors.text}]}>{spec.label}</Text>
          <TextField value={draft[spec.field] ?? field.value} onChangeText={next => {if (owned() && !busy) setDraft(current => ({...current, [spec.field]: next}));}}
            editable={!busy} maxLength={spec.maxLength} accessibilityLabel={spec.label}/>
          {provenance ? <Text style={[browseType(theme, 11.5, 500), {color: theme.colors.textTertiary}]}>{provenance}</Text> : null}
        </View>;
      })}
    </View> : null}
    </LoadingBoundary>
    {exact(data) && manifest && version ? <View style={{marginTop: 16, gap: 8}}>
      <Button variant="glass" onPress={() => {void reread();}} loading={rereadBusy} disabled={busy || coverBusy || rereadBusy}>{t('next.reader.readFileAgain')}</Button>
      {rereadFailed ? <Text style={[browseType(theme, 12, 500), {color: theme.colors.record}]}>{t('error.unknown')}</Text> : null}
    </View> : null}
    {exact(data) && data.artworkRoles.includes('cover') ? <View style={{marginTop: 16, gap: 8}}>
      <Text style={[browseType(theme, 14, 700), {color: theme.colors.text}]}>{t('web.metadata.tabArtwork')}</Text>
      {data.artwork.candidates.filter(candidate => candidate.role === 'cover').map(candidate => <View key={candidate.id} style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <Text numberOfLines={2} style={[browseType(theme, 12.5, 500), {color: theme.colors.textSecondary, flex: 1}]}>{candidate.attribution || candidate.provider}</Text>
        {candidate.current ? <Text style={[browseType(theme, 12, 600), {color: theme.colors.textTertiary}]}>{t('profile.current')}</Text>
          : <Button variant="text" size="small" disabled={coverBusy || busy || rereadBusy || !!coverOpening} onPress={() => {void selectCover(candidate.id);}}>{t('web.metadata.useIt')}</Button>}
      </View>)}
      {manifest?.format === 'cbz' && manifest.pageNames?.length ? <Button variant="glass" disabled={coverBusy || busy || rereadBusy || !!coverOpening} onPress={openCover}>{t('next.reader.chooseCoverPage')}</Button> : null}
      {coverFailed ? <Text style={[browseType(theme, 12, 500), {color: theme.colors.record}]}>{t('error.unknown')}</Text> : null}
    </View> : null}
    {coverOpening ? <RetainedOverlay permit={paintPermit} allowedTypes={paintTypes}><BottomSheet open onOpenChange={open => {if (!open) dismissCover(coverOpening);}} title={t('next.reader.chooseCoverPage')}
      description={t('next.reader.ownerOnly')} testID="native-read-cover-page" keyboardAvoiding>
      <View style={{gap: 10}}>
        <Text style={[browseType(theme, 13, 500), {color: theme.colors.textSecondary}]}>{t('next.reader.pageCount', {count: coverOpening.pageCount})}</Text>
        {coverPages?.opening === coverOpening && coverPages.phase === 'failed' ? <Text style={[browseType(theme, 12, 500), {color: theme.colors.record}]}>{t('error.unknown')}</Text> : null}
        <LoadingBoundary ready={!(coverPages?.opening === coverOpening && coverPages.phase === 'loading')} immediate={coverPages?.opening === coverOpening && coverPages.phase === 'ready'} skeleton={<SkeletonBlock width="100%" height={132}/>}>
        {coverPages?.opening === coverOpening && coverPages.phase === 'ready' && coverPages.pages.length ? <ScrollView horizontal showsHorizontalScrollIndicator={false}
          contentContainerStyle={{gap: 8, paddingRight: 4}}>
          {coverPages.pages.map(page => {
            const selected = pageInput.value.trim() === String(page.index + 1);
            return <Focusable key={page.index} accessibilityRole="button" accessibilityLabel={`${t('next.reader.page')} ${page.index + 1}`}
              onPress={() => {if (!coverBusy) setPage(String(page.index + 1));}} style={{width: 84, gap: 4}}>
              <View style={{width: 84, height: 118, borderRadius: 8, overflow: 'hidden', borderWidth: selected ? 2 : 0, borderColor: theme.colors.accent}}>
                <Image source={{uri: page.uri}} style={{width: '100%', height: '100%'}} resizeMode="cover"/>
              </View>
              <Text style={[browseType(theme, 11.5, 600), {color: selected ? theme.colors.text : theme.colors.textSecondary, textAlign: 'center'}]}>{t('next.reader.page')} {page.index + 1}</Text>
            </Focusable>;
          })}
        </ScrollView> : null}
        </LoadingBoundary>
        <TextField value={pageInput.value} onChangeText={setPage} editable={!coverBusy} keyboardType="number-pad"
          accessibilityLabel={t('next.reader.page')} maxLength={4}/>
        <Button onPress={() => {void choosePage(coverOpening, pageInput);}} loading={coverBusy} disabled={coverBusy || !pageInput.value.trim()}>{t('web.metadata.useIt')}</Button>
        {coverFailed ? <Text style={[browseType(theme, 12, 500), {color: theme.colors.record}]}>{t('error.unknown')}</Text> : null}
      </View>
    </BottomSheet></RetainedOverlay> : null}
  </BottomSheet></RetainedOverlay>;
}
