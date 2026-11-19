import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {TitleDetails, TitleFrame, TitleSection} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import {useTitle} from '../../shared/title/useTitle';

const t = defaultI18n.t;

/** A film's title page on a TV (tv-detail frames 1 to 4): `TitleFrame` filled from the title controller. Every section is
 * drawn and focus walks down them, More like this's rows then Details; the frame folds the hero as focus leaves the
 * buttons. A TV keeps nothing for offline, so the page offers no Download. */
export function FilmPage(props: NativeTitleScreenProps) {
  const view = useTitle({...props, download: undefined});
  if (view.retired) return null;
  const {about, people} = view.details;
  return <TitleFrame testID="watch-title-screen" back={{label: t('action.back'), onPress: props.back, testID: 'watch-title-back'}}
    art={<AmbientArt treatment="title" source={view.art.source} foreground={view.art.foreground}/>}
    kicker={view.kicker} title={view.title} metadata={<HeroMetadata facts={view.facts}/>}
    // tv-detail 1 and 2 name it My List whether it is on or off; on, it carries the accent tick.
    primary={view.primary} actions={view.actions.map(action => action.testID === 'watch-title-my-list' ? {...action, label: t('next.controls.myList')} : action)}
    tagline={view.tagline} synopsis={view.synopsis} credits={view.credits} tabs={view.tabs} state={view.state}>
    {view.related.map(row => <TitleSection key={row.testID ?? row.title} tab="related"><PosterRow {...row}/></TitleSection>)}
    {/* detail-tv-4: About carries the whole synopsis and the tagline beside the facts. */}
    <TitleSection tab="details"><TitleDetails {...view.details}
      about={about || view.synopsis ? {title: about?.title ?? props.aboutLabel, rows: about?.rows ?? [], body: view.synopsis, note: view.tagline} : undefined} people={people && {...people, note: t('next.detail.castThenCrew'),
        // detail-tv-4 draws no See all: on TV the row holds everyone read so far and pages as focus reaches its end.
        groups: view.people.groups, seeAll: undefined, more: view.people.more && !view.people.more.loading ? view.people.more.onPress : undefined}}/></TitleSection>
  </TitleFrame>;
}
