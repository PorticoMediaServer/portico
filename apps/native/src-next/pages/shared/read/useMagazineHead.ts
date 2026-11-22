import {useRef} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ReadingPeriodicalIssue} from '../../../../../../packages/client-core/src';
import {part, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import type {NativeReadPeriodicalState} from '../../../screens/read/read-periodical-model';
import {issueMonth} from '../../../screens/read/periodical-presentation';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {IssueOffer} from './useReadPeriodical';
import type {ReadingActionsPort} from './useReadingActions';
import {usePeriodicalActions} from './usePeriodicalActions';

const t = defaultI18n.t;

/** A magazine archive's head as a title page's (phone-read 24): its facts (publisher, how often it comes out, the year
 * range and issue count), Read with the issue it opens ("Read December 1929"), My List for that issue
 * and ⋯ with its menu (Download is there, as on a book's page). */
export function useMagazineHead({state, issue, offer, actions, runtime, onOpen}: {state: NativeReadPeriodicalState; issue?: ReadingPeriodicalIssue;
  offer: IssueOffer; actions: ReadingActionsPort; runtime: NativeShellRuntime; onOpen: (issue: ReadingPeriodicalIssue) => void}) {
  const anchor = useRef<View>(null), more = useRef<View>(null);
  const target = issue ? offer(issue, anchor) : undefined;
  const {listed, busy, toggleListed} = usePeriodicalActions(issue ?? ({item: {id: ''}} as ReadingPeriodicalIssue), target, actions.actions, runtime.reportError);
  const years = state.years.map(value => value.value).sort();
  const range = years.length > 1 ? t('next.reader.yearRange', {from: years[0], to: years[years.length - 1]}) : years[0];
  const frequency = state.frequency ? t('next.reader.frequency', {frequency: state.frequency}) : '';
  const facts: FactPart[] = [
    ...(state.publisher ? [part('publisher', state.publisher, 'context')] : []),
    ...(frequency ? [part('frequency', frequency, 'identity')] : []),
    ...(range ? [part('years', range)] : []),
    ...(state.issueCount !== undefined ? [part('issues', t('next.reader.issueCount', {count: state.issueCount}))] : []),
  ];
  const month = issueMonth(issue?.issueDate);
  const showMenu = () => {const menu = issue ? offer(issue, more) : undefined; if (menu) actions.actions.show(menu);};
  return {
    facts,
    primary: issue ? {label: month ? t('next.reader.readIssue', {issue: month}) : t('next.reader.read'), disabled: issue.item.inventoryAvailable !== true,
      onPress: () => onOpen(issue)} : undefined,
    listed: listed ? {icon: listed.listed.listed ? 'i-check' as const : 'i-plus' as const, disabled: busy,
      label: t(listed.listed.listed ? 'next.home.removeMyList' : 'next.home.addMyList'), onPress: toggleListed, anchorRef: anchor} : undefined,
    more: issue && target ? {icon: 'i-more' as const, label: t('next.controls.more'), onPress: showMenu, anchorRef: more} : undefined,
  };
}
