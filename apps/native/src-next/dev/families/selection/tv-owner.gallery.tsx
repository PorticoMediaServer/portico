import React, {useRef, useState} from 'react';
import {View} from 'react-native';
import type {Gallery} from '../../gallery-registry';
import {selectionNativeFixtures as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/selection-native-fixtures';
import {selectionFixtures as sharedFixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/selection-fixtures';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {RepairFieldSpec} from '../../../../../../packages/client-core/src/metadata-repair';
import type {DeletePreview, DeleteResult} from '../../../../../../packages/client-core/src/administration';
import {Button} from '../../../kit/controls/Button';
import {OverlaysProvider} from '../../../kit/overlays';
import {ServerAudienceTag} from '../../../kit/shell';
import {BulkEditor, DeleteMediaDialog, MixedEditExplainer, SelectionBar, type BulkChanges, type BulkFieldValue, type BulkResultView, type SelectionAction, type SelectionActionOffer} from '../../../kit/selection';
import {selectionText} from '../../../kit/selection/copy';

// Isolated DEV simulations using only existing canonical reference copy. These
// exercise controlled callbacks; they are not server outcomes or device proof.
type Scenario = 'editor' | 'editResult' | 'mixed' | 'noFields' | 'more' | 'deleteSeveral' | 'deleteBlocked' | 'deleteLimit';
const commonExcluded: readonly RepairFieldSpec[] = [
  {field: 'title', label: defaultI18n.t('web.metadata.field.title'), group: 'general', type: 'text', maxLength: 300, bulk: false},
  {field: 'sortTitle', label: defaultI18n.t('web.metadata.field.sortTitle'), group: 'general', type: 'text', maxLength: 300, bulk: false},
  {field: 'description', label: defaultI18n.t('web.metadata.field.description'), group: 'text', type: 'multiline', maxLength: 20000, bulk: false},
];
const movieExcluded: readonly RepairFieldSpec[] = [...commonExcluded,
  {field: 'originalTitle', label: defaultI18n.t('web.metadata.field.originalTitle'), group: 'general', type: 'text', maxLength: 300, bulk: false},
  {field: 'tagline', label: defaultI18n.t('web.metadata.field.tagline'), group: 'text', type: 'text', maxLength: 300, bulk: false},
  {field: 'releaseDate', label: defaultI18n.t('web.metadata.field.releaseDate'), group: 'general', type: 'date', maxLength: 10, bulk: false},
];
const textFields = ['contentRating', 'studio', 'country', 'edition', 'metadataLanguage'] as const;
const movieSchema: readonly RepairFieldSpec[] = [
  ...textFields.map(field => ({field, label: fixture.fields[field], group: 'general' as const, type: 'text' as const, maxLength: 300, bulk: true})),
  {field: 'year', label: fixture.fields.year, group: 'numbers', type: 'integer', min: 0, max: 9999, bulk: true},
  ...(['genres', 'tags', 'labels'] as const).map(field => ({field, label: fixture.fields[field], group: 'general' as const, type: 'list' as const, maxLength: 128, max: 64, bulk: true})),
];
const shared = (value: string): BulkFieldValue => ({state: 'shared', distinctCount: 1, truncated: false, value, values: [{value, count: 4}], entries: []});
const values: Readonly<Record<string, BulkFieldValue>> = {
  contentRating: {state: 'mixed', distinctCount: 2, truncated: false, values: [{value: fixture.values.rating, count: 3}, {value: '', count: 1}], entries: []},
  year: {state: 'mixed', distinctCount: 4, truncated: true, values: [], entries: []},
  studio: shared(fixture.values.studio), country: shared(fixture.values.country), edition: shared(''), metadataLanguage: shared(fixture.values.language), network: shared(fixture.values.network),
  genres: {state: 'mixed', distinctCount: 2, truncated: false, values: [], entries: fixture.genres.map((value, index) => ({value, count: index < 2 ? 4 : 3}))},
  tags: {state: 'mixed', distinctCount: 2, truncated: false, values: [], entries: [{value: fixture.tags[0], count: 3}]}, labels: {state: 'shared', distinctCount: 1, truncated: false, values: [], entries: []},
};
const movies = [
  {id: 'toy-story', title: fixture.movieTitles[0]}, {id: 'toy-story-2', title: fixture.movieTitles[1]},
  {id: 'toy-story-3', title: fixture.movieTitles[2]}, {id: 'toy-story-5', title: fixture.movieTitles[3]},
] as const;
function result(): BulkResultView {return {updated: 3, failed: 1, results: movies.map(movie => ({kind: 'item', id: movie.id, ok: movie.id !== 'toy-story-5', code: movie.id === 'toy-story-5' ? 'metadata_conflict' : undefined}))};}
function preview(blocked: boolean): DeletePreview {
  return {revision: 1, confirmation: '4', confirmationKind: 'count', totalFiles: 4, totalBytes: 16800000000, totalBytesText: sharedFixture['next.selection.fixture.previewBytes'], blocked: blocked ? [fixture.films] : [],
    targets: movies.map(({id, title}) => ({itemId: id, title, kind: 'movie', libraryId: 'movies', libraryName: fixture.films, allowMediaDeletion: !blocked, titles: 1, bytes: 4200000000,
      files: [{assetId: `${id}-file`, path: '', bytes: 4200000000, present: true, shared: false}], dependents: []}))};
}
function TVOwnerExample({scenario}: {scenario: Scenario}) {
  const prefix = `dev.selection.tv.${scenario}`;
  const [open, setOpen] = useState(false);
  const [changes, setChanges] = useState<BulkChanges>({});
  const [lockEdited, setLockEdited] = useState(true);
  const [receipt, setReceipt] = useState<BulkResultView | undefined>(scenario === 'editResult' ? result() : undefined);
  const [confirmation, setConfirmation] = useState('');
  const [removeFiles, setRemoveFiles] = useState(true);
  const [deletion, setDeletion] = useState<DeleteResult>();
  const [menu, setMenu] = useState<'queue' | 'watched' | 'more' | null>(null);
  const [lastAction, setLastAction] = useState<SelectionAction>();
  const [count, setCount] = useState(4);
  const [kept, setKept] = useState<string>();
  const opener = useRef<View>(null);
  const label = scenario === 'noFields' ? selectionText('next.selection.native.noBulkFields') : fixture.scenarios[scenario];
  const ownerHeader = <ServerAudienceTag audience={{authority: 'owner'}} />;
  const base = {testID: `${prefix}.surface`, open, onOpenChange: (next: boolean) => setOpen(next), returnFocusRef: opener};
  const actions: readonly SelectionActionOffer[] = (['more', 'edit', 'refresh', 'delete'] as const).map(id => ({id}));
  return <View style={{width: 1724, minHeight: 920, padding: 56, gap: 24}}>
    <Button testID={`${prefix}.open`} ref={opener} onPress={() => {if (scenario === 'more') {setCount(4); setMenu('more');} else setOpen(true);}}>{label}</Button>
    {lastAction ? <Button testID={`${prefix}.action-result`} onPress={() => setOpen(true)}>{selectionText(`next.selection.native.action.${lastAction}`)}</Button> : null}
    {kept ? <Button testID={`${prefix}.kept-result`} onPress={() => {setOpen(true); setKept(undefined);}}>{fixture.groups[['movie', 'episode', 'album', 'song'].indexOf(kept)]}</Button> : null}
    {scenario === 'more' && count > 0 ? <SelectionBar testID={`${prefix}.bar`} count={count} selectAllLabel={defaultI18n.t('next.selection.selectAll', {count: 4})} onSelectAll={() => setCount(4)} onCancel={() => {setMenu(null); setCount(0);}} actions={actions} menu={menu} onMenuChange={setMenu} ownerTag={ownerHeader}
      onAction={action => {setLastAction(action); setMenu(null); if (action === 'edit' || action === 'delete') setOpen(true);}} /> : null}
    {scenario === 'mixed' || scenario === 'noFields' ? <MixedEditExplainer {...base} total={4} description={scenario === 'noFields' ? selectionText('next.selection.native.noBulkFields') : fixture.mixedDescription}
      groups={scenario === 'noFields' ? [{kind: 'season', label: defaultI18n.t('next.selection.season', {count: 4}), count: 4, icon: 'i-tvlib'}] : fixture.groups.map((label, index) => ({kind: ['movie', 'episode', 'album', 'song'][index]!, label, count: 1, icon: (['i-film', 'i-tvlib', 'i-disk', 'i-music'] as const)[index]!}))}
      onKeepOnly={kind => setKept(kind)} /> : scenario.startsWith('delete') || scenario === 'more' && lastAction === 'delete' ? <DeleteMediaDialog {...base} owner ownerHeader={ownerHeader} selectionCount={scenario === 'deleteLimit' ? 240 : 4} preview={preview(scenario === 'deleteBlocked')}
      confirmation={confirmation} onConfirmationChange={setConfirmation} removeFiles={removeFiles} onRemoveFilesChange={setRemoveFiles} result={deletion}
      onDelete={supplied => setDeletion({revision: supplied.revision + 1, removed: supplied.targets.length, failed: 0, bytesDeleted: removeFiles ? supplied.totalBytes : 0, filesRetained: !removeFiles, receipts: supplied.targets.map(target => ({itemId: target.itemId, title: target.title, removed: true, filesDeleted: removeFiles ? target.files.length : 0, filesKept: removeFiles ? 0 : target.files.length, bytes: target.bytes, code: ''}))})} />
      : <BulkEditor {...base} ownerHeader={ownerHeader} title={fixture.editTitle} count={4} saveLabel={fixture.saveTitle}
        schema={movieSchema} singleItemFields={movieExcluded} fieldLabel={field => field.label} values={values} changes={changes} onChangesChange={setChanges} lockEdited={lockEdited} onLockEditedChange={setLockEdited}
        summary={Object.entries(changes).flatMap(([id, change]) => {const field = movieSchema.find(spec => spec.field === id)!.label; return 'value' in change ? [{id, text: defaultI18n.t('next.selection.scalarChange', {field, value: change.value || selectionText('next.selection.native.notSet'), count: 4})}]
          : [...(change.add.length ? [{id: `${id}-add`, text: defaultI18n.t('next.selection.listAdd', {field, values: defaultI18n.list(change.add), count: 4})}] : []), ...(change.remove.length ? [{id: `${id}-remove`, text: defaultI18n.t('next.selection.listRemove', {field, values: defaultI18n.list(change.remove), count: 4})}] : [])];})} onSave={() => setReceipt(result())} result={receipt} resultEntries={movies}
        onRetry={ids => setReceipt(current => {if (!current) return current; const results = current.results.map(row => ids.includes(row.id) && !row.ok ? {...row, ok: true, code: undefined} : row); const updated = results.filter(row => row.ok).length; return {results, updated, failed: results.length - updated};})} />}
  </View>;
}
const scenarios: readonly Scenario[] = ['editor', 'editResult', 'mixed', 'noFields', 'more', 'deleteSeveral', 'deleteBlocked', 'deleteLimit'];
const galleries: readonly Gallery[] = scenarios.map(scenario => ({id: `selection-tv-${scenario}`, title: 'next.selection.native.gallery', platforms: ['tv'], render: function TVGallery() {return <OverlaysProvider><TVOwnerExample scenario={scenario} /></OverlaysProvider>;}}));
export default galleries;
