import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt} from '../../../kit/artwork';
import {EpisodeList, PanelBlock, PeopleSheet, TitleDetails, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow, SeasonBar} from '../../../kit/layout';
import {SeasonSheet} from '../../../kit/layout/SeasonSheet';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import {PlayOnButton} from '../../../shell/PlayOnButton';
import {useTitle} from '../../shared/title/useTitle';

const t = defaultI18n.t;

/** A film's or a show's title page on a phone and a tablet (phone-detail d1 and d2, tablet f6 to f8): `TitleFrame`
 * filled from the title controller. A show opens on Episodes (the season bar, the season's episodes with the opened one
 * in place, the season sheet); More like this is the related rows; Details is About, Cast and crew and a film's collection. */
export function TitlePage(props: NativeTitleScreenProps) {
  const view = useTitle(props);
  if (view.retired) return null;
  return <><TitleFrame testID="watch-title-screen" back={{label: t('action.back'), onPress: props.back, testID: 'watch-title-back'}}
    topControl={props.onPlayOn ? <PlayOnButton playOn={props.playOn} variant="round" testID="watch-title-play-on" onPress={anchor => props.onPlayOn?.(anchor)}/> : undefined}
    art={<AmbientArt treatment="title" source={view.art.source} foreground={view.art.foreground} onReady={view.art.onReady} onError={view.art.onError}/>} artRef={view.art.ref}
    scrollMemory={view.scroll} onEnd={view.onEnd} selectionBar={props.selectionBar}
    kicker={view.kicker} title={view.title} metadata={<HeroMetadata facts={view.facts}/>}
    primary={view.primary} actions={view.actions} tagline={view.tagline} synopsis={view.synopsis} credits={view.credits}
    tabs={view.tabs} state={view.state}>
    {view.tabs.value === 'episodes' && view.episodes ? <><PanelBlock><SeasonBar {...view.episodes.bar}/></PanelBlock><EpisodeList {...view.episodes.list}/></>
      : view.tabs.value === 'details' ? <TitleDetails {...view.details}/> : view.related.map(row => <PosterRow key={row.title} {...row}/>)}
  </TitleFrame>
  {view.episodes && <SeasonSheet {...view.episodes.sheet}/>}
  <PeopleSheet {...view.people}/></>;
}
