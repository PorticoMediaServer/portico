import React from 'react';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard} from '../../../kit/frames';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {useTVServerDetails} from '../../shared/settings/useTVServerDetails';

export function ServerDetails(props: Parameters<typeof useTVServerDetails>[0]) {
  const {page, runtime} = props;
  const {t, snapshot, error, retry, empty, previous, next} = useTVServerDetails(props);
  if (error !== undefined) return <StateView testID={`tv-server-${page}-error`} {...runtime.error(error, retry)}/>;
  if (!snapshot) return <StateView title={t('status.loading')}/>;
  return <>
    {snapshot.facts.length ? <SectionCard>
      {snapshot.facts.map((fact, index) => <SettingsRow key={fact.id} readable first={index === 0}
        testID={`tv-server:${page}:${fact.id}`} label={fact.label}
        description={[fact.value, fact.detail].filter(Boolean).join('\n')}
        accessibilityLabel={[fact.label, fact.value, fact.detail].filter(Boolean).join('. ')}/>)}
    </SectionCard> : <StateView title={empty}/>}
    {previous && <PanelBlock><Button focusId={`tv-server:${page}:previous`} variant="glass" align="stretch" onPress={previous}>{t('web.pager.previous')}</Button></PanelBlock>}
    {next && <PanelBlock><Button focusId={`tv-server:${page}:next`} variant="glass" align="stretch" onPress={next}>{t('web.pager.next')}</Button></PanelBlock>}
  </>;
}
