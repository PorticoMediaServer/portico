import React, {useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {HomeSelectionController} from '../../../../packages/client-core/src/home-selection/index';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {BulkEditor, bulkReceiptView} from '../kit/selection';
import {Artwork} from '../kit/artwork';
import {Button} from '../kit/controls/Button';
import {Dialog} from '../kit/overlays';
import {ServerAudienceTag} from '../kit/shell';
import {useTheme} from '../kit/theme';
import {memberTitle, metadataFieldLabel} from './selection-display';
import {nativeBulkChanges, nativeBulkDraft, nativeBulkValues} from './selection-editor';
import type {NativeShellRuntime} from './runtime';
import {tvFocusIds} from './tv-focus';

const t = defaultI18n.t;

/** Server schema and retained receipts remain with the one shared controller. */
export function HomeBulkEditor({controller, runtime, reportError}: {
  controller: HomeSelectionController; runtime: NativeShellRuntime; reportError: (error: unknown) => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), editor = state.editor;
  if (!editor) return null;
  const editorId = theme.isTV ? tvFocusIds(runtime.getSnapshot().authority).surface('editor') : undefined;
  const close = () => controller.closeEditor();
  const run = (work: Promise<unknown>) => {void work.catch(reportError);};
  const error = editor.error ? presentError(editor.error, 'home', {operation: 'action'}).body : undefined;
  // An ambiguous write retries its original body/id; re-previewing would lose that operation.
  if (!editor.preview || editor.phase === 'uncertain' || editor.phase === 'error' || editor.phase === 'conflict') return <Dialog testID={editorId} open
    onOpenChange={open => {if (!open) close();}} title={t('next.selection.native.action.edit')}
    footer={<View style={{flexDirection: 'row', gap: 10}}>
      <Button testID={editorId ? `${editorId}-close` : undefined} variant="glass" size="compact" onPress={close}>{t('action.close')}</Button>
      {editor.phase !== 'loading' && <Button testID={editorId ? `${editorId}-retry` : undefined} size="compact" onPress={() => run(editor.phase === 'uncertain' ? controller.saveEditor()
        : editor.receipts.length ? controller.retryEditor(editor.draft) : controller.reloadEditor())}>{t('action.tryAgain')}</Button>}
    </View>}>
    <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.textSecondary, paddingTop: 16}]}>{error ?? t('next.states.loading')}</Text>
  </Dialog>;
  const preview = editor.preview, count = state.displayMembers.length, kind = preview.contentKind;
  // The current metadata preview route admits owners. A changed role ends this authority lifetime.
  if (runtime.getSnapshot().account.viewerRole !== 'owner') return null;
  const changes = nativeBulkChanges(editor.draft), lockEdited = editor.draft.lockEdited === true;
  const summary = Object.entries(changes).flatMap(([name, change]) => {
    const field = preview.schema.find(value => value.field === name);
    if (!field) throw new Error('metadata_change_schema_required');
    const label = metadataFieldLabel(field);
    return 'value' in change ? [{id: name, text: t('next.home.native.changeScalar', {field: label, value: change.value || t('next.selection.native.notSet'), count: preview.total})}]
      : [...(change.add.length ? [{id: `${name}:add`, text: t('next.home.native.addValues', {field: label, values: defaultI18n.list(change.add), count: preview.total})}] : []),
        ...(change.remove.length ? [{id: `${name}:remove`, text: t('next.home.native.removeValues', {field: label, values: defaultI18n.list(change.remove), count: preview.total})}] : [])];
  });
  const resultEntries = state.displayMembers.flatMap(member => {
    const title = memberTitle(member, runtime);
    if (!title || !('id' in member.item)) return [];
    const source = runtime.artwork(member.entry?.posterUrl);
    return [{id: member.item.id, title, artwork: source ? <Artwork source={source} width={30} height={45}/> : undefined}];
  });
  return <BulkEditor testID={editorId} open onOpenChange={open => {if (!open) close();}} title={t('next.home.native.editSelection', {count, kind})}
    count={count} saveLabel={t('next.home.native.saveSelection', {count: preview.total, kind})} schema={preview.schema} values={nativeBulkValues(preview)}
    changes={changes} onChangesChange={value => controller.setEditorDraft(nativeBulkDraft(value, lockEdited))}
    lockEdited={lockEdited} onLockEditedChange={value => controller.setEditorDraft(nativeBulkDraft(changes, value))}
    onSave={() => run(controller.saveEditor())} pending={editor.phase !== 'ready' && editor.phase !== 'result'} error={error}
    result={editor.phase === 'result' || editor.receipts.length ? bulkReceiptView(editor.receipts) : undefined} resultEntries={resultEntries}
    onRetry={ids => run(controller.retryEditor(editor.draft, ids))} summary={summary}
    singleItemFields={preview.singleItemFields} fieldLabel={metadataFieldLabel}
    ownerHeader={<ServerAudienceTag audience={{authority: 'owner'}} layout={theme.layoutMode === 'phone' ? 'phone' : 'desktop'}/>} />;
}
