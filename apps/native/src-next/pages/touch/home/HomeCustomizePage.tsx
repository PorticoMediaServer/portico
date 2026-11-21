import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {Button, Switch} from '../../../kit/controls';
import {HomeAlwaysOn, HomeDragHandle, HomeLayoutCard, HomeLayoutRow, HomeLayoutScroll, HomeSavedViewChoices, HomeViewMark} from '../../../kit/home/HomeLayout';
import {Banner, BottomSheet, Dialog} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {StateView} from '../../../kit/states';
import {useHomeCustomize, type HomeCustomizeProps} from '../../shared/home/useHomeCustomize';

const t = defaultI18n.t;

/** Customize Home on a phone or tablet (collections 16-20): the tall sheet with "Customize Home" and its line of help, Home's
 * rows in one card (handle, name, what it shows, its switch or "Always on Home"), Add a saved view, then Reset to default and
 * Save at its foot. Choosing a saved view swaps the card for the views; a layout changed elsewhere asks to reload. */
export function HomeCustomizePage(props: HomeCustomizeProps) {
  const {copy, i18n, views, summary, notice, noticeAction, renderError, testID, controller} = props;
  const view = useHomeCustomize(props), {state, choosing, blocked, displayed, heights, positions, targetY, dragGeometry, scrollGesture} = view;
  const footer = choosing
    ? <OverlayFootRow layout="halves">
      {views?.hasMore ? <Button focusId="views-more" testID={`${testID}-views-more`} variant="text" disabled={views.loading} onPress={views.loadMore}>{i18n.t('action.showMore')}</Button> : null}
      <Button focusId="views-cancel" testID={`${testID}-views-cancel`} variant="glass" onPress={view.returnToAdd}>{i18n.t('action.cancel')}</Button>
    </OverlayFootRow>
    : state.pickedId ? undefined : <OverlayFootRow layout="halves">
      <Button ref={view.resetButton} focusId="reset" variant="text" disabled={blocked || state.conflict} onPress={view.startReset}>{i18n.t('action.resetToDefault')}</Button>
      <Button focusId="save" disabled={blocked || state.conflict || !state.dirty} loading={state.pending} onPress={view.save}>{copy.save}</Button>
    </OverlayFootRow>;
  const rows = <HomeLayoutCard dropAt={state.pickedId ? targetY : undefined}>
    {displayed.map((row, index) => {
      const lifted = row.id === state.pickedId, title = serverTextLabel(i18n, row.titleText, row.title);
      const originalY = positions.get(row.id), shift = lifted && targetY !== undefined && originalY !== undefined ? targetY - originalY : 0;
      return <HomeLayoutRow key={row.id} first={index === 0} lifted={lifted} shift={shift} height={heights.get(row.id)} onMeasure={(y, height) => view.measure(row.id, y, height)}
        handle={row.reorderable ? <HomeDragHandle controller={controller} id={row.id} disabled={blocked || !!state.pickedId && !lifted} geometry={dragGeometry}
          scrollGesture={scrollGesture} testID={`${testID}-handle-${row.id}`}/> : undefined}
        leading={row.savedView ? <HomeViewMark/> : undefined} title={title} hidden={row.hidden}
        detail={row.savedView ? summary(row.savedView) : serverTextLabel(i18n, row.availabilityText)}
        rowRef={node => {if (node) view.rowRefs.current.set(row.id, node); else view.rowRefs.current.delete(row.id); if (lifted) view.picked.current = node; if (index === 0) view.first.current = node;}}
        testID={`${testID}-row-${row.id}`} disabled={blocked || !!state.pickedId && !lifted} accessibilityLabel={title}
        accessibilityActions={row.reorderable ? [{name: 'increment', label: t('home.customize.moveDown', {title})}, {name: 'decrement', label: t('home.customize.moveUp', {title})}] : []}
        onAccessibilityAction={event => {
          if (event.nativeEvent.actionName !== 'increment' && event.nativeEvent.actionName !== 'decrement') return;
          if (!lifted && !controller.pick(row.id)) return;
          if (controller.getSnapshot().pickedId !== row.id) return;
          controller.move(event.nativeEvent.actionName === 'increment' ? 1 : -1); controller.drop();
        }}
        trailing={<>
          {row.savedView && <Button testID={`${testID}-remove-${row.id}`} variant="icon" size="compact" icon="i-close" iconOnly
            accessibilityLabel={t('home.customize.removeRow', {title})} disabled={blocked || !!state.pickedId} onPress={() => view.removeRow(row.id, index)}/>}
          {row.required ? <HomeAlwaysOn label={copy.alwaysOn}/> : row.hideable && <Switch focusId={`${row.id}:visibility`} checked={!row.hidden}
            disabled={blocked || !!state.pickedId} accessibilityLabel={t('home.customize.showRow', {title})} onCheckedChange={() => controller.toggle(row.id)}/>}
        </>}/>;
    })}
  </HomeLayoutCard>;
  const body = choosing && views
    ? views.loading && !views.items.length ? null : views.error ? views.renderError(views.error, views.retry)
      : <HomeSavedViewChoices disabled={views.loading || blocked} summary={summary} views={views.items.filter(item => !state.rows.some(row => row.id === `view:${item.id}`))}
        hasMore={views.hasMore} i18n={i18n} firstRef={view.firstView} onChoose={view.chooseView}/>
    : <>
      {notice && <Banner message={notice} tone="info" action={noticeAction ? {label: noticeAction.label, onPress: noticeAction.onPress} : undefined}/>}
      {state.showConflict && <StateView variant="inline" icon="i-warn" title={copy.conflictTitle} body={copy.conflictBody}
        primaryAction={{id: 'reload', label: copy.reload, disabled: state.pending || state.phase === 'loading', onPress: () => {void controller.load();}}}
        secondaryAction={{id: 'keep-editing', label: copy.keepEditing, onPress: view.keepEditing}}/>}
      {state.error && !state.showConflict && renderError?.(state.error, view.retryError)}
      {rows}
      {!state.pickedId && <Button ref={view.addView} focusId="add-view" variant="text" icon="i-plus" disabled={blocked}
        onPress={() => {if (views) view.setChoosing(true); props.onAddView();}}>{copy.addView}</Button>}
    </>;
  return <>
    <BottomSheet open onOpenChange={open => {if (!open) view.back();}} title={choosing ? t('home.customize.addView') : copy.title}
      description={t(choosing ? 'home.customize.chooseView' : 'home.customize.subtitle')} testID={testID} initialFocusRef={choosing ? view.firstView : view.first}
      defaultDetent={1} showClose={false} footer={footer} scrollable={false}><HomeLayoutScroll locked={!!state.pickedId} scrollRef={view.scroller} gesture={scrollGesture}>{body}</HomeLayoutScroll></BottomSheet>
    <Dialog testID={`${testID}-reset-confirm`} open={!!view.resetOpening} onOpenChange={open => {if (!open) view.closeReset(view.resetOpening);}}
      title={t('confirm.resetHome.title')} description={t('confirm.resetHome.body')} initialFocusRef={view.cancelReset} returnFocusRef={view.resetButton} showClose={false}
      footer={<OverlayFootRow layout="end">
        <Button ref={view.cancelReset} focusId="reset-cancel" testID={`${testID}-reset-cancel`} variant="glass" onPress={() => view.closeReset(view.resetOpening)}>{t('action.cancel')}</Button>
        <Button focusId="reset-confirm" testID={`${testID}-reset-submit`} variant="danger" disabled={state.pending} onPress={() => view.confirmReset(view.resetOpening)}>{copy.reset}</Button>
      </OverlayFootRow>}>{null}</Dialog>
  </>;
}
