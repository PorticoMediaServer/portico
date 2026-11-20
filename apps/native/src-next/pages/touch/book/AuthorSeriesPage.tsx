import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, PersonCircle, missingArt} from '../../../kit/artwork';
import {AboutTable, musicScroller, PanelBlock, PanelHeading, StateRow, TitleFrame, TitleRowWindow} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {useAuthorSeries, type AuthorSeriesPageProps} from '../../shared/book/useAuthorSeries';

const t = defaultI18n.t;

/** An author's or a series' page on a phone (collections 41b, 41c): `TitleFrame` with the author's circle of their books'
 * covers or the series' first cover over the page wash, Resume the book you are in (and, for a series, Start the series
 * over), their books a window at a time (paging the server near the end), and an author's Details. */
export function AuthorSeriesPage(props: AuthorSeriesPageProps) {
  const view = useAuthorSeries(props);
  return <TitleFrame testID="native-phone-audiobook-collection" back={{label: t('action.back'), onPress: () => props.onBack?.(), testID: 'audiobook-collection-back'}}
    topAction={{icon: 'i-more', label: t('card.more', {title: view.title}), onPress: view.more, testID: 'audiobook-collection-more'}}
    art={<AmbientArt treatment="page" source={view.glow}/>}
    cover={view.series
      ? {shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size} priority
        fallback={view.cover ? undefined : missingArt({title: view.title, shape: 'square', width: size, height: size})}/>}
      : {shape: 'circle', large: true, node: size => <PersonCircle size={size} initials={view.initials} covers={view.covers} alt={view.title} visible/>}}
    kicker={{label: view.kicker, accent: true}} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
    primary={{icon: 'i-play', label: view.primary.label, disabled: view.primary.disabled, onPress: view.primary.onPress, testID: 'audiobook-collection-resume'}}
    actions={view.startOver ? [{icon: 'i-restart', label: t('action.startSeriesOver'), onPress: view.startOver, testID: 'audiobook-collection-start-over'}] : []}
    scroller={musicScroller({onNearEnd: view.onNearEnd, testID: 'audiobook-collection-scroll'})}>
    {view.state && <PanelBlock><StateRow {...view.state}/></PanelBlock>}
    <TitleRowWindow heading={{title: view.books.title, trailing: view.books.count}} rows={view.books.rows} testID="audiobook-collection-books"/>
    {view.continuation && <PanelBlock><StateRow {...view.continuation}/></PanelBlock>}
    {view.details && <PanelBlock><PanelHeading title={t('title.details')}/><AboutTable rows={view.details}/></PanelBlock>}
  </TitleFrame>;
}
