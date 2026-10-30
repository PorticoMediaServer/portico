import React, {useState} from 'react';
import {ScrollView, Text, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {shellNativeCases as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/shell-native-fixtures';
import type {SegmentReviewEntry} from '../../../../../../packages/client-core/src/segment-review';
import type {Gallery} from '../../gallery-registry';
import {Dialog} from '../../../kit/overlays/surface';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';
import {useTheme} from '../../../kit/theme';
import {BarList, DeviceRow, PersonRow, SegmentWindows, Sparkline, StatTiles, WaitingNotice} from '../../../kit/shell';

function StatisticsGallery() {
  const {width} = useWindowDimensions();
  const [selected, setSelected] = useState<typeof fixture.stats[number] | undefined>();
  const theme = useTheme();
  return <OverlaysProvider><ScrollView contentContainerStyle={{padding: 16, gap: 20}}><StatTiles testID="dev.shell.statistics" tiles={fixture.stats} width={Math.min(width - 32, 952)} onTilePress={id => setSelected(fixture.stats.find(tile => tile.id === id))}/><BarList rows={fixture.barRows}/></ScrollView><Dialog testID="dev.shell.statistics.detail" open={Boolean(selected)} onOpenChange={open => {if (!open) setSelected(undefined);}} title={selected?.label ?? defaultI18n.t('next.shell.native.statisticsGallery')}>{selected && <><Text style={[theme.type.title, {color: theme.colors.text}]}>{selected.value}</Text><Sparkline samples={selected.samples} min={selected.min} max={selected.max} width={Math.min(width - 80, 700)} height={170} endDot/></>}</Dialog></OverlaysProvider>;
}
function PeopleGallery() {
  const theme = useTheme();
  const [waiting, setWaiting] = useState(true);
  const [blocked, setBlocked] = useState(false);
  const [person, setPerson] = useState(false);
  return <ScrollView contentContainerStyle={{padding: theme.isTV ? 96 : 16, gap: 16}}>
    <PersonRow testID="dev.shell.people.member" {...fixture.person} role="member" onOpen={() => setPerson(!person)}/>
    <PersonRow testID="dev.shell.people.owner" {...fixture.person} role="owner"/>
    <PersonRow testID="dev.shell.people.administrator" {...fixture.person} role="administrator"/>
    {waiting && <WaitingNotice message={fixture.waitingMessage} action={{label: defaultI18n.t('web.devices.approve'), onPress: () => setWaiting(false)}}/>}
    <DeviceRow testID="dev.shell.people.device" {...fixture.device} waiting={waiting} blocked={blocked} state={waiting ? fixture.device.state : defaultI18n.t(blocked ? 'web.access.trustBlocked' : 'web.access.trustApproved')} onOpen={() => setPerson(!person)} actions={waiting ? [{id: 'approve', icon: 'i-check', label: defaultI18n.t('web.devices.approve'), onPress: () => setWaiting(false)}, {id: 'block', icon: 'i-x', label: defaultI18n.t('web.access.block'), destructive: true, onPress: () => {setBlocked(true); setWaiting(false);}}] : []}/>
    {person && <Text style={[theme.type.body, {color: theme.colors.text}]}>{fixture.person.name}</Text>}
  </ScrollView>;
}
function SegmentsGallery() {
  const theme = useTheme();
  const [entry, setEntry] = useState<SegmentReviewEntry>(fixture.segment);
  const [watchAt, setWatchAt] = useState<string | undefined>();
  const [adding, setAdding] = useState(false);
  return <OverlaysProvider><ScrollView contentContainerStyle={{padding: 20, gap: 12}}><SegmentWindows entry={entry} windows={fixture.windows} onSave={(marker, bounds) => setEntry({...entry, markers: entry.markers.map(item => item.id === marker.id ? {...item, ...bounds, edited: true} : item)})} onApprove={marker => setEntry({...entry, markers: entry.markers.map(item => item.id === marker.id ? {...item, approved: true} : item)})} onDismiss={marker => setEntry({...entry, markers: entry.markers.filter(item => item.id !== marker.id)})} onWatch={(_marker, fromUS) => setWatchAt(fromUS)} onAdd={() => setAdding(!adding)}/>{watchAt && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{watchAt}</Text>}{adding && <Text style={[theme.type.caption, {color: theme.colors.text}]}>{defaultI18n.t('web.segments.addTitle')}</Text>}</ScrollView></OverlaysProvider>;
}
const galleries: readonly Gallery[] = [
  {id: 'shell-statistics', title: 'next.shell.native.statisticsGallery', render: StatisticsGallery, platforms: ['phone', 'tablet']},
  {id: 'shell-people-devices', title: 'next.shell.native.peopleGallery', render: PeopleGallery, platforms: ['phone', 'tablet', 'tv']},
  {id: 'shell-segments', title: 'next.shell.native.segmentsGallery', render: SegmentsGallery, platforms: ['phone', 'tablet']},
];
export default galleries;
