import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, Artwork, missingArt, PersonCircle} from '../../../kit/artwork';
import {musicScroller, StateRow, TitleFrame, TitleRowWindow} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {useAuthorSeries, type AuthorSeriesPageProps} from '../../shared/book/useAuthorSeries';

const t = defaultI18n.t;

export function AuthorSeriesPage(props: AuthorSeriesPageProps) {
  const view = useAuthorSeries(props, true);
  const retry = view.state?.kind === 'error' ? view.state.action?.focusId : undefined;
  const more = {icon: 'i-more' as const, label: t('card.more', {title: view.title}), onPress: view.more, testID: 'audiobook-collection-more'};
  return <TitleFrame testID="native-tv-audiobook-collection"
    back={{label: t('action.back'), onPress: () => props.onBack?.(), testID: 'audiobook-collection-back'}}
    startFocus={view.primary.disabled ? retry ?? more.testID : 'audiobook-collection-resume'}
    art={<AmbientArt treatment="cover" source={view.glow}/>}
    cover={view.series
      ? {shape: 'square', node: size => <Artwork source={view.cover} width={size} height={size} priority
        fallback={view.cover ? undefined : missingArt({title: view.title, shape: 'square', width: size, height: size})}/>}
      : {shape: 'circle', node: size => <PersonCircle size={size} initials={view.initials} ambientCovers={view.ambientCovers} alt={view.title} visible/>}}
    kicker={{label: view.kicker, accent: true}} title={view.title} byline={view.byline} metadata={<HeroMetadata facts={view.facts}/>}
    primary={{icon: 'i-play', label: view.primary.label, disabled: view.primary.disabled, onPress: view.primary.onPress, testID: 'audiobook-collection-resume'}}
    actions={[...(view.startOver ? [{icon: 'i-restart' as const, label: t('action.startSeriesOver'), labelled: true, onPress: view.startOver,
      testID: 'audiobook-collection-start-over'}] : []), more]}
    scroller={musicScroller({onNearEnd: view.onNearEnd, testID: 'audiobook-collection-scroll'})}
    beside={<>
      {view.state && <StateRow {...view.state}/>}
      <TitleRowWindow inset={false} longPressOnly rows={view.books.rows} onFocusRow={index => {if (index >= view.books.rows.length - 3) view.onNearEnd();}} testID="audiobook-collection-books"/>
      {view.continuation && <StateRow {...view.continuation}/>}
    </>}/>;
}
