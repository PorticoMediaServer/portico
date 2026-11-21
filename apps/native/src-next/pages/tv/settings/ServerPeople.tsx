import React from 'react';
import {SectionCard} from '../../../kit/frames/parts';
import {SettingsRow} from '../../../kit/shell/SettingsControls';
import {StateView} from '../../../kit/states/StateView';
import {useTVServerPeople, type TVServerPeopleProps} from '../../shared/settings/useTVServerPeople';

export function ServerPeople(props: TVServerPeopleProps) {
  const {runtime} = props;
  const {t} = runtime.text;
  const people = useTVServerPeople(props);
  if (!people) return <StateView title={t('status.loading')}/>;
  const {groups, state, retry} = people;
  return <>{groups.map(({id, label, empty, icon, rows}) => {
    const {pending, loaded, error} = state[id];
    return <SectionCard key={id} label={label}>
      {rows.map((row, index) => <SettingsRow key={row.id} testID={`tv-people:${row.id}`} readable first={index === 0}
        label={row.label} description={row.facts} value={row.status} icon={icon}
        accessibilityLabel={[row.label, ...row.facts, row.status].filter(Boolean).join('. ')}/>)}
      {loaded && !rows.length && <StateView title={empty}/>}
      {pending && <StateView title={t('status.loading')}/>}
      {error !== undefined && <StateView {...runtime.error(error, () => retry(id))} testID={`tv-people-${id}-error`}/>}
    </SectionCard>;
  })}</>;
}
