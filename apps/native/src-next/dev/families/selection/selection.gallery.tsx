import React, {useEffect, useState} from 'react';
import {View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {Gallery} from '../../gallery-registry';
import {selectionNativeFixtures as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/selection-native-fixtures';
import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src/index';
import type {DeletePreview, DeleteResult} from '../../../../../../packages/client-core/src/administration';
import type {RepairFieldSpec} from '../../../../../../packages/client-core/src/metadata-repair';
import type {BulkReceipt} from '../../../../../../packages/client-core/src/metadata-bulk';
import {CoverMosaic, Artwork, type ArtworkSource} from '../../../kit/artwork/index';
import {PosterCard} from '../../../kit/cards/index';
import {Button} from '../../../kit/controls/Button';
import {OverlaysProvider} from '../../../kit/overlays/index';
import {alpha} from '../../../kit/overlays/surface';
import {OwnerTag, ServerMark, ServerAudienceTag} from '../../../kit/shell/index';
import {SelectionBar, SelectionPhoneActionBar, BulkEditor, PlaylistPicker, CollectionPicker, DeleteMediaDialog, MixedEditExplainer, bulkReceiptView, type BulkChanges, type BulkEditorProps, type DeleteMediaDialogProps, type DestinationPickerProps, type PickerEntry, type PickerResult, type SelectionAction, type SelectionActionOffer} from '../../../kit/selection/index';
import {SelectionText} from '../../../kit/selection/surface';
import {useTheme} from '../../../kit/theme';
import {referenceFields, referenceSingleItemFields} from './reference-fields';

// Static reference observations are DEV-only. Mutations require genuine supplied evidence.
type SelectionGallerySnapshot = Readonly<{picker?: DestinationPickerProps; editor?: BulkEditorProps; deletion?: DeleteMediaDialogProps}>;
type SelectionGalleryEvidence = {getSnapshot: () => SelectionGallerySnapshot; subscribe: (changed: () => void) => () => void};
type SelectionGalleryEvent = Readonly<{kind: 'open-change'; scenario: Scenario; surface: string; next: boolean; reason: Parameters<DeleteMediaDialogProps['onOpenChange']>[1]}>
  | Readonly<{kind: 'action'; scenario: Scenario; action: SelectionAction | 'keep-only'; value?: string}>
  | Readonly<{kind: 'mutation-attempt'}>;
declare global {var __porticoNativeSelectionEvidence: SelectionGalleryEvidence | undefined; var __PORTICO_NATIVE_SELECTION_PREVIEW_URL__: string | undefined; var __porticoNativeSelectionTrace: SelectionGalleryEvent[] | undefined;}
function traceSelection(event: SelectionGalleryEvent) {
  const events = globalThis.__porticoNativeSelectionTrace;
  if (!events) return;
  events.push(event);
  if (events.length > 64) events.splice(0, events.length - 64);
}
function requireSelectionEvidence(): never {
  traceSelection({kind: 'mutation-attempt'});
  throw new Error('Selection gallery mutations require complete props from the actual fenced controller evidence adapter.');
}
const reference = `${globalThis.__PORTICO_NATIVE_FAMILY_ASSET_BASE__ ?? 'http://100.120.50.24:8787/assets/'}pool/movies/`;
const art = (index: number): ArtworkSource => ({uri: `${reference}${['toy-story.jpg', 'toy-story-2.jpg', 'toy-story-3.jpg', 'toy-story-5.jpg'][index]}`, cacheKey: `selection-reference-${index}`});
const schemas: readonly RepairFieldSpec[] = [
  {field: 'contentRating', label: fixture.fields.contentRating, group: 'general', type: 'text', bulk: true},
  {field: 'studio', label: fixture.fields.studio, group: 'general', type: 'text', bulk: true},
  {field: 'network', label: fixture.fields.network, group: 'general', type: 'text', bulk: true},
  {field: 'country', label: fixture.fields.country, group: 'general', type: 'text', bulk: true},
  {field: 'year', label: fixture.fields.year, group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true},
  {field: 'edition', label: fixture.fields.edition, group: 'general', type: 'text', bulk: true},
  {field: 'metadataLanguage', label: fixture.fields.metadataLanguage, group: 'general', type: 'text', bulk: true},
  {field: 'genres', label: fixture.fields.genres, group: 'general', type: 'list', bulk: true},
  {field: 'tags', label: fixture.fields.tags, group: 'general', type: 'list', bulk: true},
  {field: 'labels', label: fixture.fields.labels, group: 'general', type: 'list', bulk: true},
];
const movieEntries = [
  {id: 'toy-story', title: fixture.movieTitles[0], artworkIndex: 0}, {id: 'toy-story-2', title: fixture.movieTitles[1], artworkIndex: 1},
  {id: 'toy-story-3', title: fixture.movieTitles[2], artworkIndex: 2}, {id: 'toy-story-5', title: fixture.movieTitles[3], artworkIndex: 3},
] as const;
const playlistKeys = ['family-movies', 'weekend-movies', 'watch-again'] as const;
const collectionKeys = ['animation', 'adventure', 'favorites'] as const;
const actionIds: readonly SelectionAction[] = ['play', 'queue', 'playlist', 'myList', 'more', 'playNext', 'playLater', 'watched', 'unwatched', 'collection', 'favorite', 'download', 'edit', 'refresh', 'delete'];
const actions: readonly SelectionActionOffer[] = actionIds.map(id => ({id}));
type Scenario = keyof typeof fixture.scenarios;
function SelectionExample({scenario, deniedDestination = false, previewReadURL}: {scenario: Scenario; deniedDestination?: boolean; previewReadURL?: string}) {
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const prefix = `dev.selection.${deniedDestination ? 'picker-admission' : scenario}`;
  const [evidence, setEvidence] = useState(() => globalThis.__porticoNativeSelectionEvidence?.getSnapshot());
  useEffect(() => {
    const source = globalThis.__porticoNativeSelectionEvidence;
    if (!source) return;
    const update = () => setEvidence(source.getSnapshot());
    const unsubscribe = source.subscribe(update);
    update();
    return unsubscribe;
  }, []);
  const [previewRead, setPreviewRead] = useState<{loading: boolean; error?: string}>({loading: !!previewReadURL});
  useEffect(() => {
    if (!previewReadURL) return;
    const request = new AbortController();
    void fetch(previewReadURL, {signal: request.signal}).then(response => {
      if (!response.ok) setPreviewRead({loading: false, error: defaultI18n.t('error.unavailable')});
      else setPreviewRead({loading: false});
    }, () => {if (!request.signal.aborted) setPreviewRead({loading: false, error: defaultI18n.t('error.unavailable')});});
    return () => request.abort();
  }, [previewReadURL]);
  const [selected, setSelected] = useState<readonly string[]>(scenario === 'circles' ? [movieEntries[0].id] : theme.isTV ? movieEntries.slice(0, 3).map(movie => movie.id) : movieEntries.map(movie => movie.id));
  const [menu, setMenu] = useState<'queue' | 'watched' | 'more' | null>(scenario === 'queue' ? 'queue' : scenario === 'more' ? 'more' : null);
  const [open, setOpen] = useState(true);
  const [creating, setCreating] = useState(scenario === 'newPlaylist');
  const [name, setName] = useState(scenario === 'newPlaylist' ? fixture.newName : '');
  const [query, setQuery] = useState('');
  const [lastAction, setLastAction] = useState<SelectionAction | 'navigate'>();
  const [changes, setChanges] = useState<BulkChanges>(scenario === 'episodes' ? {metadataLanguage: {value: fixture.values.language}} : scenario === 'tracks' ? {year: {value: String(2020)}} : {contentRating: {value: fixture.values.rating}, tags: {add: [fixture.tags[1]], remove: []}});
  const [lockEdited, setLockEdited] = useState(true);
  const [bulkReceipts] = useState<readonly BulkReceipt[]>(scenario === 'editResult' ? [receipt()] : []);
  const [removeFiles, setRemoveFiles] = useState(scenario !== 'deleteOff');
  const [confirmation, setConfirmation] = useState(scenario === 'deleteSeveral' || scenario === 'deleteOff' ? '4' : '');
  const [deleteResult] = useState<DeleteResult | undefined>(scenario === 'deleteResult' ? deletionResult() : undefined);
  const [pickerResult, setPickerResult] = useState<PickerResult>();
  const [page, setPage] = useState(0);
  const size = theme.isTV ? 76 : 48;
  const collection = scenario === 'collection';
  const entries: PickerEntry[] = (collection ? fixture.collections : fixture.playlists).map((title, index) => ({id: (collection ? collectionKeys : playlistKeys)[index], title, size: [12, 6, 9][index], canAdd: !deniedDestination || index !== 1, ...(index === 0 ? {alreadyIn: 2} : {}),
    artwork: <CoverMosaic covers={[art(index), art((index + 1) % 4), art((index + 2) % 4), art((index + 3) % 4)]} shape="square" width={size} height={size} />}));
  const pickerCount = theme.isTV ? 3 : 4;
  const destination = entries[0];
  const referenceFailure = {id: movieEntries[pickerCount - 1].id, title: fixture.movieTitles[pickerCount - 1], reason: fixture.failedAdd};
  const suppliedResult: PickerResult | undefined = pickerResult ?? (scenario === 'partialPlaylist' && destination ? {destination, inserted: pickerCount - 1, failed: 1, total: pickerCount, failures: [referenceFailure], complete: true} : undefined);
  // Bounded in-memory reference directory, not the current-page search or a server receipt.
  const matching = entries.filter(entry => entry.title.toLocaleLowerCase('en-US').includes(query.toLocaleLowerCase('en-US')));
  const pageSize = theme.isTV ? 2 : 8;
  const windowStart = page * pageSize, windowEntries = matching.slice(windowStart, windowStart + pageSize);
  const bulkResult = bulkReceipts.length ? bulkReceiptView(bulkReceipts) : undefined;
  const close = (next: boolean, reason: Parameters<DeleteMediaDialogProps['onOpenChange']>[1]) => {traceSelection({kind: 'open-change', scenario, surface: `${prefix}.surface`, next, reason}); setOpen(next);};
  const onAction = (action: SelectionAction) => {traceSelection({kind: 'action', scenario, action}); setLastAction(action);};
  const moreOwnerTag = theme.platform === 'phone' ? <View testID={`${prefix}.more.owner-tag`} style={{alignSelf: 'center', paddingVertical: 3, paddingHorizontal: 8, borderRadius: 6, backgroundColor: alpha(theme.colors.ink, .6)}}>
    <SelectionText size={12} weight={700} style={{color: theme.colors.textSecondary, includeFontPadding: false}}>{defaultI18n.t('next.selection.onlyYou')}</SelectionText>
  </View> : <OwnerTag compact variant="badge" />;
  const barProps = {testID: `${prefix}.bar`, count: selected.length, subtitle: fixture.films, selectionLabel: defaultI18n.t('next.selection.movie', {count: selected.length}), selectAllLabel: fixture.selectAll, onSelectAll: () => setSelected(movieEntries.map(movie => movie.id)), onCancel: () => setSelected([]), actions, onAction, menu, onMenuChange: setMenu, topInset: insets.top, ownerTag: moreOwnerTag};
  const base = {testID: `${prefix}.surface`, open, onOpenChange: close, ownerHeader: <View style={{paddingHorizontal: 20, paddingTop: 6, paddingBottom: 0, flexDirection: 'row', alignItems: 'center', gap: 8}}><ServerMark variant="editorPhone" /><OwnerTag compact variant="badge" /></View>};
  const isPicker = ['playlist', 'newPlaylist', 'partialPlaylist', 'collection'].includes(scenario);
  const isEditor = ['editor', 'episodes', 'tracks', 'editResult'].includes(scenario);
  const editorKind = scenario === 'episodes' ? 'episode' : scenario === 'tracks' ? 'song' : 'movie';
  const editorCount = editorKind === 'episode' ? 3 : 4;
  const editorSchema = schemas.filter(field => editorKind === 'episode' ? ['contentRating', 'network', 'year', 'metadataLanguage', 'tags', 'labels'].includes(field.field) : editorKind === 'song' ? ['year', 'metadataLanguage', 'genres', 'tags', 'labels'].includes(field.field) : field.field !== 'network');
  const t = defaultI18n.t;
  const summary = Object.entries(changes).flatMap(([id, change]) => {
    const field = editorSchema.find(value => value.field === id);
    if (!field) return [];
    if ('value' in change) return [{id, text: t('next.selection.scalarChange', {field: field.label, value: change.value, count: editorCount})}];
    return [
      ...(change.add.length ? [{id: `${id}-add`, text: t('next.selection.listAdd', {field: field.label, values: defaultI18n.list(change.add), count: editorCount})}] : []),
      ...(change.remove.length ? [{id: `${id}-remove`, text: t('next.selection.listRemove', {field: field.label, values: defaultI18n.list(change.remove), count: editorCount})}] : []),
    ];
  });
  return <View testID={`${prefix}.frame`} accessibilityLabel={fixture.scenarios[scenario]} style={{backgroundColor: theme.colors.ink, width: theme.isTV ? 1724 : dimensions.width, height: theme.isTV ? 700 : dimensions.height, marginLeft: -theme.gutter}}>
    <>
      <SelectionBar {...barProps} />
      <View style={{flexDirection: 'row', gap: 18, padding: 20, flexWrap: 'wrap'}}>{movieEntries.map(movie => <PosterCard testID={`${prefix}.movie.${movie.id}`} key={movie.id} title={movie.title} width={theme.isTV ? 200 : 108} artwork={art(movie.artworkIndex)} density="grid"
        onNavigate={() => setLastAction('navigate')} onMore={() => setMenu('more')}
        selection={{active: selected.length > 0, selected: selected.includes(movie.id), onEnter: () => setSelected([movie.id]), onToggle: () => setSelected(current => current.includes(movie.id) ? current.filter(id => id !== movie.id) : [...current, movie.id])}} />)}</View>

      <View style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><SelectionPhoneActionBar {...barProps} bottomInset={Math.max(34, insets.bottom)} /></View>
    </>
    {!open && !pickerResult ? <Button testID={`${prefix}.open`} size="compact" onPress={() => setOpen(true)}>{fixture.scenarios[scenario]}</Button> : null}
    {lastAction ? <SelectionText>{formatMessage(fixture.lastAction, {action: lastAction}, {locale: 'en-US'})}</SelectionText> : null}
    {isPicker ? React.createElement(collection ? CollectionPicker : PlaylistPicker, {
      ...base, count: pickerCount, selectionLabel: defaultI18n.t('next.selection.movie', {count: pickerCount}), entries: windowEntries, query, onQueryChange: value => {setQuery(value); setPage(0);}, showFind: theme.isTV || matching.length > pageSize,
      creating, onCreatingChange: setCreating, name, onNameChange: setName,
      hasNext: windowStart + windowEntries.length < matching.length, hasPrevious: page > 0,
      onNext: windowStart + windowEntries.length < matching.length ? () => setPage(current => current + 1) : undefined,
      onPrevious: page > 0 ? () => setPage(current => current - 1) : undefined,
      onCreateAndAdd: requireSelectionEvidence, onAdd: requireSelectionEvidence,
      result: suppliedResult, onRetry: requireSelectionEvidence,
      onViewDestination: () => {setPickerResult(undefined);setOpen(true);}, onResultDismiss: () => setPickerResult(undefined),
      ...evidence?.picker,
    }) : null}
    {isEditor ? <BulkEditor {...base} title={scenario === 'episodes' ? fixture.episodeTitle : scenario === 'tracks' ? fixture.trackTitle : fixture.editTitle} count={editorCount} saveLabel={t('next.selection.save', {items: t(editorKind === 'episode' ? 'next.selection.episode' : editorKind === 'song' ? 'next.selection.track' : 'next.selection.movie', {count: editorCount})})}
      ownerHeader={<ServerAudienceTag audience={{authority: 'owner'}} layout={theme.isTV ? 'desktop' : 'phone'} />} schema={editorSchema}
      fieldLabel={field => field.label} values={referenceFields(editorKind)}
      changes={changes} onChangesChange={setChanges} lockEdited={lockEdited} onLockEditedChange={setLockEdited} onSave={requireSelectionEvidence} result={bulkResult}
      resultEntries={movieEntries.map(movie => ({id: movie.id, title: movie.title, artwork: <Artwork source={art(movie.artworkIndex)} width={30} height={45} />}))}
      onRetry={requireSelectionEvidence}
      singleItemFields={referenceSingleItemFields(editorKind)} summary={summary} {...evidence?.editor} /> : null}
    {scenario === 'mixed' ? <MixedEditExplainer testID={`${prefix}.surface`} open={open} onOpenChange={close} total={4} description={fixture.mixedDescription} groups={fixture.groups.map((label, index) => ({kind: ['movie', 'episode', 'album', 'song'][index], label, count: 1, icon: (['i-film', 'i-tv', 'i-album', 'i-note'] as const)[index]}))} onKeepOnly={kind => {traceSelection({kind: 'action', scenario, action: 'keep-only', value: kind}); setSelected([kind]);}} /> : null}
    {scenario.startsWith('delete') ? <DeleteMediaDialog {...base} owner selectionCount={scenario === 'deleteLimit' ? 240 : scenario === 'deleteOne' || scenario === 'deleteShow' || scenario === 'deleteBlocked' ? 1 : 4}
      preview={previewReadURL ? null : deletionPreview(scenario)} loading={previewRead.loading} error={previewRead.error} removeFiles={removeFiles} onRemoveFilesChange={setRemoveFiles} confirmation={confirmation} onConfirmationChange={setConfirmation}
      onDelete={requireSelectionEvidence} result={deleteResult} renderArtwork={id => <Artwork source={art(movieEntries.find(movie => movie.id === id)?.artworkIndex ?? 0)} width={24} height={36} />} {...evidence?.deletion} /> : null}
  </View>;
}
function receipt(): BulkReceipt {return {operationId: 'selection-gallery-edit', updated: 3, failed: 1, results: movieEntries.map(movie => ({id: movie.id, kind: 'item', ok: movie.id !== 'toy-story-5', code: movie.id === 'toy-story-5' ? 'metadata_conflict' : undefined}))};}
function deletionPreview(scenario: Scenario): DeletePreview {
  const single = scenario === 'deleteOne' || scenario === 'deleteBlocked' || scenario === 'deleteShow';
  return {revision: 1, confirmation: single ? scenario === 'deleteShow' ? fixture.showTitle : fixture.movieTitles[3] : '4', confirmationKind: single ? 'title' : 'count', totalFiles: scenario === 'deleteShow' ? 9 : single ? 1 : 4,
    totalBytes: 16800000000, totalBytesText: single ? '4.1 GB' : '16.8 GB', blocked: scenario === 'deleteBlocked' ? [fixture.films] : [],
    targets: (single ? [movieEntries[3]] : movieEntries).map(movie => ({itemId: scenario === 'deleteShow' ? 'reference-show' : movie.id, title: scenario === 'deleteShow' ? fixture.showTitle : movie.title, kind: scenario === 'deleteShow' ? 'show' : 'movie', libraryId: 'movies', libraryName: fixture.films, allowMediaDeletion: scenario !== 'deleteBlocked', titles: scenario === 'deleteShow' ? 9 : 1, bytes: 4100000000, files: [{assetId: `${movie.id}-file`, path: '', bytes: 4100000000, present: true, shared: false}], dependents: [{kind: 'playlist', count: 2, description: ''}, {kind: 'collection', count: 1, description: ''}, {kind: 'progress', count: 3, description: ''}]}))};
}
function deletionResult(): DeleteResult {return {revision: 2, removed: 3, failed: 1, bytesDeleted: 12700000000, filesRetained: false, receipts: movieEntries.map(movie => ({itemId: movie.id, title: movie.title, removed: movie.id !== 'toy-story-5', filesDeleted: movie.id === 'toy-story-5' ? 0 : 1, filesKept: movie.id === 'toy-story-5' ? 1 : 0, bytes: 4100000000, code: movie.id === 'toy-story-5' ? 'file_not_removed' : ''}))};}
const galleries: readonly Gallery[] = [
  ...(globalThis.__PORTICO_NATIVE_SELECTION_PREVIEW_URL__ ? [{id: 'selection-preview-unavailable', title: 'next.selection.native.gallery' as const, platforms: ['phone'] as const, render: function PreviewUnavailable() {return <OverlaysProvider><SelectionExample scenario="deleteOne" previewReadURL={globalThis.__PORTICO_NATIVE_SELECTION_PREVIEW_URL__} /></OverlaysProvider>;}}] : []),
  ...(Object.keys(fixture.scenarios) as Scenario[]).map((scenario): Gallery => ({id: `selection-${scenario}`, title: 'next.selection.native.gallery', platforms: ['circles', 'bar', 'queue', 'more', 'playlist', 'newPlaylist', 'partialPlaylist', 'collection'].includes(scenario) ? ['phone', 'tablet', 'tv'] : ['phone', 'tablet'], render: function GalleryExample() {return <OverlaysProvider><SelectionExample scenario={scenario} /></OverlaysProvider>;}})),
  {id: 'selection-picker-admission', title: 'next.selection.native.gallery', platforms: ['phone', 'tablet', 'tv'], render: function PickerAdmission() {return <OverlaysProvider><SelectionExample scenario="playlist" deniedDestination /></OverlaysProvider>;}},
];
export default galleries;
