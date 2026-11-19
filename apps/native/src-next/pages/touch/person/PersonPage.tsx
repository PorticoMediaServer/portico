import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, PersonCircle} from '../../../kit/artwork';
import {ChoiceChips, PanelBlock, PanelHeading, posterGridScroller, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {Menu} from '../../../kit/overlays';
import {usePerson, type PersonPageProps} from '../../shared/person/usePerson';

const t = defaultI18n.t;

/** A person's page on a phone and a tablet (phone-detail d3 frame 7): `TitleFrame` with their circle over a glow from
 * their films, Known for, and every credit on this server as a windowed poster grid filtered by All, Cast or Crew. */
export function PersonPage(props: PersonPageProps) {
  const view = usePerson(props);
  return <>
    <TitleFrame testID="native-person" back={{label: t('action.back'), onPress: props.onBack, testID: 'person-back'}}
      topAction={view.share ? {icon: 'i-more', label: view.share.label, onPress: () => view.share?.onOpenChange(true), testID: `${props.focusScope}.share`} : undefined}
      art={<AmbientArt treatment="library" source={view.glow}/>}
      cover={{shape: 'circle', node: size => <PersonCircle size={size} initials={view.initials} source={view.portrait}/>}}
      title={view.name} metadata={<HeroMetadata facts={view.facts}/>}
      synopsis={view.biography?.text} readMore={view.biography?.readMore}
      scroller={posterGridScroller(view.credits.grid)}>
      {view.knownFor && <PosterRow {...view.knownFor}/>}
      <PanelBlock>
        <PanelHeading title={view.credits.title} trailing={view.credits.count}/>
        <ChoiceChips items={view.credits.choices}/>
        {view.credits.state && <StateRow {...view.credits.state}/>}
      </PanelBlock>
    </TitleFrame>
    {view.share && <Menu testID={`${props.focusScope}.share-menu`} open={view.share.open} onOpenChange={view.share.onOpenChange} title={view.name} items={view.share.items}/>}
  </>;
}
