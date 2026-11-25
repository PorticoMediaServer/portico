import React, {useRef, useState} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AmbientArt, PersonCircle} from '../../../kit/artwork';
import {Chip} from '../../../kit/controls';
import {PanelBlock, PosterWindowRow, StateRow, TitleFrame} from '../../../kit/frames';
import {HeroMetadata, PosterRow} from '../../../kit/layout';
import {Menu} from '../../../kit/overlays';
import {usePerson, type PersonPageProps} from '../../shared/person/usePerson';

const t = defaultI18n.t;

/** A person's page on a TV (tv-detail frame 10, `f9`): `TitleFrame` with their circle at the left (their photo, else their
 * initials over a blur of their posters), their departments over the name, Born and where, and the count of their titles on
 * this server, the biography; then Known for, and their credits as a row per department with Filters (All, Cast, Crew) on
 * the first. Focus starts on the biography's Read more (the old screen's start), else the first title they are known for,
 * so the head stays in view; after a filter (the page is drawn again for its read) it starts back on Filters. */
export function PersonPage(props: PersonPageProps) {
  const view = usePerson(props), filtersRef = useRef<View>(null), [filters, setFilters] = useState(false);
  const role = view.credits.choices.find(choice => choice.selected && choice.id !== 'select');
  const tools = <Chip ref={filtersRef} focusId="person-role-filter" testID="person-role-filter" selected={role?.id !== 'all'} onPress={() => setFilters(true)}>{t('next.browse.native.filters')}</Chip>;
  return <>
    <TitleFrame testID="native-person" back={{label: t('action.back'), onPress: props.onBack, testID: 'person-back'}} startFocus={props.role !== 'all' ? 'person-role-filter' : view.biography ? 'title-read-more' : view.knownFor?.items[0]?.key}
      art={<AmbientArt treatment="person" source={view.glow}/>}
      cover={{shape: 'circle', node: size => <PersonCircle size={size} initials={view.initials} source={view.portrait} ambientCovers={view.mosaic}/>}}
      kicker={view.roles ? {label: view.roles} : undefined} title={view.name}
      metadata={<HeroMetadata facts={[...view.facts.filter(fact => fact.id !== 'roles'), ...(view.birthPlace ? [{id: 'place', text: view.birthPlace, role: 'context' as const}] : []),
        {id: 'count', text: view.credits.count, role: 'context' as const}]}/>}
      synopsis={view.biography?.text} readMore={view.biography?.readMore}>
      {/* Each part is a section focus walks down: Known for, then a row per department, then a failed read's Try again. */}
      {view.knownFor && <PosterRow {...view.knownFor}/>}
      {view.credits.rows.map((row, index) => <PosterWindowRow key={row.id} {...row} tools={index === 0 ? tools : undefined}/>)}
      {view.credits.state && <PanelBlock><StateRow {...view.credits.state}/></PanelBlock>}
    </TitleFrame>
    <Menu testID={`${props.focusScope}.role-filter`} open={filters} onOpenChange={setFilters} title={t('next.browse.native.filters')} returnFocusRef={filtersRef}
      items={view.credits.choices.filter(choice => choice.id !== 'select').map(choice => ({id: choice.id, label: choice.label, selected: choice.selected, onSelect: choice.onPress}))}/>
  </>;
}
