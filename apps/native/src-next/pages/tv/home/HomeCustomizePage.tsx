import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {Button} from '../../../kit/controls';
import {HomeSavedViewChoices} from '../../../kit/home/HomeLayout';
import {TVHomeAlwaysOn, TVHomeLayoutConflict, TVHomeLayoutFoot, TVHomeLayoutHandle, TVHomeLayoutList, TVHomeLayoutNotice, TVHomeLayoutPanel,
  TVHomeLayoutRemove, TVHomeLayoutRow, TVHomeMoveHints, TVHomeResetButton, TVHomeRowSwitch, TVHomeViewMark} from '../../../kit/home/HomeTVLayout';
import {Dialog} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {useHomeCustomize, type HomeCustomizeProps} from '../../shared/home/useHomeCustomize';

const t = defaultI18n.t;

/** Customize Home on TV (final3-tv): the panel at the right with Home's rows (grip, name, what it shows, its switch or
 * "Always on Home"), Add a saved view, then Reset to default and Save. OK on a row lifts it; ▲▼ move it, OK drops it and
 * Back puts it back. Choosing a saved view swaps the rows for the views; a layout changed elsewhere asks to reload.
 * While a row is lifted every grip is off, its own too: the lifted row is the one control left, so focus stays on it as it
 * is laid out again (Android otherwise moved focus to its grip, where Up and Down did nothing). */
export function HomeCustomizePage(props: HomeCustomizeProps) {
  const {copy, i18n, views, summary, notice, noticeAction, renderError, testID, controller} = props;
  const view = useHomeCustomize(props), {state, choosing, blocked, displayed, heights, positions, targetY} = view;
  const footer = choosing
    ? <TVHomeLayoutFoot start={views?.hasMore ? <Button focusId="views-more" testID={`${testID}-views-more`} variant="text" disabled={views.loading} onPress={views.loadMore}>{i18n.t('action.showMore')}</Button> : undefined}
      end={<Button focusId="views-cancel" testID={`${testID}-views-cancel`} variant="glass" onPress={view.returnToAdd}>{i18n.t('action.cancel')}</Button>}/>
    : state.pickedId ? undefined : <TVHomeLayoutFoot
      start={<Button ref={view.resetButton} focusId="reset" variant="glass" disabled={blocked || state.conflict} onPress={view.startReset}>{i18n.t('action.resetToDefault')}</Button>}
      end={<Button focusId="save" disabled={blocked || state.conflict || !state.dirty} loading={state.pending} onPress={view.save}>{copy.save}</Button>}/>;
  const rows = <TVHomeLayoutList scrollRef={view.scroller} dropAt={state.pickedId ? targetY : undefined}>
    {displayed.map((row, index) => {
      const lifted = row.id === state.pickedId, title = serverTextLabel(i18n, row.titleText, row.title);
      const originalY = positions.get(row.id), shift = lifted && targetY !== undefined && originalY !== undefined ? targetY - originalY : 0;
      return <TVHomeLayoutRow key={row.id} lifted={lifted} shift={shift} height={heights.get(row.id)} minHeight={view.rowHeight} onMeasure={(y, height) => view.measure(row.id, y, height)}
        handle={row.reorderable ? <TVHomeLayoutHandle focusId={`${row.id}:handle`} testID={`${testID}-handle-${row.id}`} label={t('next.home.layoutMoveAction')}
          lifted={lifted} disabled={blocked || !!state.pickedId} onPress={() => view.pickRow(row.id)}/> : undefined}
        leading={row.savedView ? <TVHomeViewMark/> : undefined} title={title} hidden={row.hidden}
        detail={row.savedView ? summary(row.savedView) : serverTextLabel(i18n, row.availabilityText)}
        rowRef={node => {if (node) view.rowRefs.current.set(row.id, node); else view.rowRefs.current.delete(row.id); if (lifted) view.picked.current = node; if (index === 0) view.first.current = node;}}
        focusId={row.id} testID={`${testID}-row-${row.id}`} disabled={blocked || !!state.pickedId && !lifted}
        preferredFocus={index === 0 && !state.showConflict && !choosing} onPress={() => view.pickRow(row.id)} accessibilityLabel={title}
        accessibilityActions={row.reorderable ? [{name: 'increment', label: t('home.customize.moveDown', {title})}, {name: 'decrement', label: t('home.customize.moveUp', {title})}] : []}
        onAccessibilityAction={event => {
          if (event.nativeEvent.actionName !== 'increment' && event.nativeEvent.actionName !== 'decrement') return;
          if (!lifted && !controller.pick(row.id)) return;
          if (controller.getSnapshot().pickedId !== row.id) return;
          controller.move(event.nativeEvent.actionName === 'increment' ? 1 : -1); controller.drop();
        }}
        trailing={<>
          {row.savedView && <TVHomeLayoutRemove focusId={`${row.id}:remove`} testID={`${testID}-remove-${row.id}`} label={t('home.customize.removeRow', {title})}
            disabled={blocked || !!state.pickedId} onPress={() => view.removeRow(row.id, index)}/>}
          {row.required ? <TVHomeAlwaysOn label={copy.alwaysOn}/> : row.hideable && <TVHomeRowSwitch focusId={`${row.id}:visibility`} checked={!row.hidden}
            disabled={blocked || !!state.pickedId} accessibilityLabel={t('home.customize.showRow', {title})} onCheckedChange={() => controller.toggle(row.id)}/>}
        </>}/>;
    })}
    {!state.pickedId && <Button ref={view.addView} focusId="add-view" variant="text" icon="i-plus" disabled={blocked}
      onPress={() => {if (views) view.setChoosing(true); props.onAddView();}}>{copy.addView}</Button>}
  </TVHomeLayoutList>;
  const body = choosing && views
    ? views.loading && !views.items.length ? null : views.error ? views.renderError(views.error, views.retry)
      : <HomeSavedViewChoices disabled={views.loading || blocked} summary={summary} views={views.items.filter(item => !state.rows.some(row => row.id === `view:${item.id}`))}
        hasMore={views.hasMore} i18n={i18n} firstRef={view.firstView} onChoose={view.chooseView}/>
    : <>
      {(notice || noticeAction) && <TVHomeLayoutNotice message={notice} action={noticeAction && <Button variant="text" onPress={noticeAction.onPress}>{noticeAction.label}</Button>}/>}
      {state.showConflict && <TVHomeLayoutConflict title={copy.conflictTitle} body={copy.conflictBody} actions={<>
        <Button focusId="reload" ref={view.reload} disabled={state.pending || state.phase === 'loading'} onPress={() => {void controller.load();}}>{copy.reload}</Button>
        <Button focusId="keep-editing" variant="glass" onPress={view.keepEditing}>{copy.keepEditing}</Button>
      </>}/>}
      {state.error && !state.showConflict && renderError?.(state.error, view.retryError)}
      {rows}
    </>;
  return <>
    {state.pickedId && <TVHomeMoveHints keys={[['▲▼', copy.move], [t('next.layout.ok'), copy.drop], [t('action.back'), copy.putBack]]}/>}
    <TVHomeLayoutPanel title={choosing ? t('home.customize.addView') : copy.title}
      description={choosing ? t('home.customize.chooseView') : state.showConflict ? undefined : copy.instruction} onBack={view.back}
      initialFocusRef={choosing ? view.firstView : view.first} footer={footer} testID={testID}>{body}</TVHomeLayoutPanel>
    <Dialog testID={`${testID}-reset-confirm`} open={!!view.resetOpening} onOpenChange={open => {if (!open) view.closeReset(view.resetOpening);}}
      title={t('confirm.resetHome.title')} description={t('confirm.resetHome.body')} initialFocusRef={view.cancelReset} returnFocusRef={view.resetButton} showClose={false}
      footer={<OverlayFootRow layout="end">
        <Button ref={view.cancelReset} focusId="reset-cancel" testID={`${testID}-reset-cancel`} variant="glass" onPress={() => view.closeReset(view.resetOpening)}>{t('action.cancel')}</Button>
        <TVHomeResetButton label={copy.reset} testID={`${testID}-reset-submit`} disabled={state.pending} onPress={() => view.confirmReset(view.resetOpening)}/>
      </OverlayFootRow>}>{null}</Dialog>
  </>;
}
