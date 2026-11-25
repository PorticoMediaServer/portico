import type {RefObject} from 'react';
import {useLayoutEffect, useMemo, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import type {ReadingPeriodicalIssue, ReadingYearChoice} from '../../../../../../packages/client-core/src';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {askNativeDownload} from '../../../screens/downloads/request';
import type {NativeReadPeriodical} from '../../../screens/read/read-periodical-model';
import type {NativeReadingActionOffer} from '../../../screens/read/reading-actions';
import {periodicalHeroIssue} from '../../../screens/read/periodical-presentation';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRoute} from '../../../shell/routes';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useReadingActions} from './useReadingActions';

type PeriodicalRoute = Extract<NativeShellRoute, {kind: 'read-periodical'}>;
export type ReadPeriodicalProps = {
  controller: NativeReadPeriodical;
  route: PeriodicalRoute;
  context: NativeShellBootstrapContext;
  runtime: NativeShellRuntime;
  onBack: () => void;
};
export type IssueOffer = (issue: ReadingPeriodicalIssue, anchorRef?: RefObject<View | null>) => NativeReadingActionOffer | undefined;

export function useReadPeriodical({controller, route, context, runtime}: ReadPeriodicalProps) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const actions = useReadingActions(context, runtime, controller);
  useLayoutEffect(() => {
    if (!controller.arm()) return;
    void controller.load().catch(runtime.reportError);
    return () => controller.release();
  }, [controller, route, runtime.reportError]);
  const archive = useMemo(() => ({
    retry: () => {void controller.retry().catch(runtime.reportError);},
    next: () => {void controller.next(state).catch(runtime.reportError);},
    ensureYear: (year: ReadingYearChoice, more?: boolean) => {void controller.ensureYear(state, year, more).catch(runtime.reportError);},
    selectYear: (year: ReadingYearChoice) => {void controller.selectYear(state, year).catch(runtime.reportError);},
    setOrder: (order: 'newest' | 'oldest') => {void controller.setOrder(state, order).catch(runtime.reportError);},
    selectMonth: (month: string) => {void controller.selectMonth(state, month).catch(runtime.reportError);},
    selectDate: (date: string) => {void controller.selectDate(state, date).catch(runtime.reportError);},
    nextDate: () => {void controller.nextDate(state).catch(runtime.reportError);},
    moreMonths: () => {void controller.moreMonths(state).catch(runtime.reportError);},
  }), [controller, state, runtime.reportError]);
  const open = (issue: ReadingPeriodicalIssue) => {void controller.open(state, issue).catch(runtime.reportError);};
  const offer: IssueOffer = (issue, anchorRef) => {
    const lifetime = controller.itemLifetime(issue);
    return lifetime ? {item: issue.item, lifetime, anchorRef, open: () => open(issue),
      ...(nativePublicationReadsAvailable() ? {download: () => askNativeDownload({title: issue.item.title, itemId: issue.item.id, reading: true, anchorRef, lifetime})} : {})} : undefined;
  };
  const magazine = route.publicationType === 'magazine', selected = periodicalHeroIssue(state, magazine);
  // The archive's library, its name a link to it (phone-read 24's "Magazine · Books").
  const known = context.libraries().find(value => value.id === route.libraryId);
  const library = known ? {name: known.name, open: () => runtime.onDestination({kind: 'library', libraryId: known.id})} : undefined;
  return {state, actions, archive, open, offer, magazine, selected, library, backdrop: runtime.ambient(selected?.item.coverUrl)};
}

export type PeriodicalArchiveActions = ReturnType<typeof useReadPeriodical>['archive'];
