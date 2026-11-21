import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {CoverCard} from '../../../kit/cards';
import {Button} from '../../../kit/controls';
import {PeriodicalShelf} from '../../../kit/reading/PeriodicalShelf';
import {StateView} from '../../../kit/states';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeReadingPageOpening} from '../../../screens/read/reading-page-opening';
import {usePeriodicalShelf} from '../../shared/reading/usePeriodicalShelf';

const t = defaultI18n.t;

/** A reading library's Magazines or Newspapers (tablet-read 1): each publication as its latest issue's cover with the
 * date and the number of issues; it opens the publication's archive. */
export function ReadingPeriodicalShelf(props: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime; libraryId: string;
  kind: 'magazine' | 'newspaper'; opening: NativeReadingPageOpening}) {
  const shelf = usePeriodicalShelf(props), {state} = shelf;
  if (state.phase === 'error') return <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
    primaryAction={{id: 'retry', label: t('next.states.tryAgain'), onPress: shelf.retry}}/>;
  if (state.phase === 'ready' && !state.groups.length) return <StateView variant="inline" title={t('title.nothingHere')}/>;
  return <PeriodicalShelf testID={`reading-${props.kind}-shelf`} cards={width => shelf.cards.map(card => <CoverCard key={card.key} testID={`reading-periodical-${card.key}`}
    kind={props.kind === 'magazine' ? 'periodical' : 'newspaper'} width={width} title={card.title} caption={card.caption} artwork={card.cover} onNavigate={card.open}/>)}
    footer={shelf.more && <Button variant="glass" onPress={shelf.more}>{t('action.showMore')}</Button>}/>;
}
