import {defaultI18n} from '@i18n';
import type {HomeSelectionController, HomeSelectionSnapshot} from '@core/home-selection/index.ts';
import type {SelectionAction} from '@core/selection-model/index.ts';
import type {SelectionActions} from '../kit/selection';

/** Start before the menu's close event clears its canonical target. */
export function runSelectionCommand(work: () => void | Promise<unknown>, reportError: (error: unknown) => void): void {
  try { void Promise.resolve(work()).catch(reportError); } catch (error) { reportError(error); }
}

/** The renderer may retry only the actual bounded failure window the controller will submit. */
export function retryPickerWindow(controller: Pick<HomeSelectionController, 'retryPicker' | 'getSnapshot'>, ids: readonly string[]) {
  const picker = controller.getSnapshot().picker, failures = picker?.receipt?.failures;
  if (picker?.phase !== 'partial' || !failures?.length || ids.length !== failures.length || ids.some((id, index) => id !== failures[index].itemId)) throw new Error('invalid-picker-retry-window');
  return controller.retryPicker();
}

export function retryPickerOperation(controller: Pick<HomeSelectionController, 'retryPicker' | 'getSnapshot'>) {
  if (controller.getSnapshot().picker?.phase !== 'uncertain') throw new Error('picker-operation-not-uncertain');
  return controller.retryPicker();
}

export function retryPickerError(controller: Pick<HomeSelectionController, 'retryPicker' | 'getSnapshot'>) {
  if (controller.getSnapshot().picker?.phase !== 'error') throw new Error('picker-not-in-error');
  return controller.retryPicker();
}

/** Reload delegates to the controller's retained failed subset or unchanged uncertain operation. */
export function retryEditorRead(controller: Pick<HomeSelectionController, 'reloadEditor' | 'saveEditor'>, phase: NonNullable<HomeSelectionSnapshot['editor']>['phase']) {
  return phase === 'uncertain' ? controller.saveEditor() : controller.reloadEditor();
}

/** Optional renderer callbacks follow canonical all-of rules; the controller executes every effect. */
export function selectionCallbacks(snapshot: HomeSelectionSnapshot, controller: HomeSelectionController, anchor: (element: HTMLElement) => void, reportError: (error: unknown) => void): SelectionActions {
  const ready = (action: SelectionAction) => snapshot.actions[action].state === 'ready';
  const execute = (action: SelectionAction) => runSelectionCommand(() => controller.execute(action), reportError);
  const unsupported = (action: SelectionAction) => {
    const rule = snapshot.actions[action];
    return rule.state === 'disabled' && (rule.reason === 'unsupported-sort' || rule.reason === 'preview-limit')
      ? defaultI18n.t(rule.reason === 'unsupported-sort' ? 'next.selection.unsupportedSort' : 'next.selection.previewLimit') : undefined;
  };
  return {
    ...(unsupported('play') ? {playDisabledReason: unsupported('play')} : {}),
    ...(unsupported('play-next') ? {queueDisabledReason: unsupported('play-next')} : {}),
    ...(unsupported('playlist') ? {playlistDisabledReason: unsupported('playlist')} : {}),
    ...(unsupported('delete') ? {deleteDisabledReason: unsupported('delete')} : {}),
    ...(unsupported('edit') ? {editDisabledReason: unsupported('edit')} : {}),
    ...(ready('play') ? {onPlay: () => execute('play')} : {}),
    ...(ready('play-next') && ready('play-later') ? {onQueue: (placement: 'next' | 'end') => execute(placement === 'next' ? 'play-next' : 'play-later')} : {}),
    ...(ready('playlist') ? {onPlaylist: (element: HTMLElement) => { anchor(element); runSelectionCommand(() => controller.openPicker('playlist'), reportError); }} : {}),
    ...(ready('my-list') ? {onMyList: () => execute('my-list')} : {}),
    ...(ready('mark-watched') && ready('mark-unwatched') ? {onWatchedChange: (watched: boolean) => execute(watched ? 'mark-watched' : 'mark-unwatched')} : {}),
    ...(snapshot.actions.edit.state === 'ready' || snapshot.actions.edit.state === 'explain' ? {onEdit: (element: HTMLElement) => { anchor(element); runSelectionCommand(() => controller.openEditor(), reportError); }, editMode: snapshot.actions.edit.state === 'ready' ? 'editable' as const : 'mixed' as const} : {}),
    ...(ready('collection') ? {onCollection: (element: HTMLElement) => { anchor(element); runSelectionCommand(() => controller.openPicker('collection'), reportError); }} : {}),
    ...(ready('favorite') ? {onFavorite: () => execute('favorite')} : {}),
    ...(ready('remove-my-list') ? {onRemoveFromMyList: () => execute('remove-my-list')} : {}),
    ...(ready('refresh') ? {onRefresh: () => execute('refresh')} : {}),
    ...(ready('delete') || snapshot.actions.delete.state === 'disabled' && snapshot.actions.delete.reason !== 'unsupported-sort' ? {onDelete: () => runSelectionCommand(() => controller.openDelete(), reportError)} : {}),
  };
}
