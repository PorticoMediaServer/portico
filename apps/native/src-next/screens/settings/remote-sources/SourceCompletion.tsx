import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import type {RemoteSource, SourceReadSettings} from '../../../../../../packages/client-core/src/remote-sources';
import {sourceChoiceComparison} from '../../../../../../packages/client-core/src/remote-source-choice';
import type {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Icon} from '../../../kit/Icon';
import {alpha} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeRemoteSourcesController} from './controller';
import {Page, Problem, remoteSourceError, sourceReadPresetLabel, useAttempt} from './RemoteSourceForms';

/** Only confirmed source, reading and assignment facts; no inferred scan progress. */
export function SourceCompletionFacts({source, profile, library, count = 0, t}: {
  source: RemoteSource; profile?: SourceReadSettings; library?: string; count?: number; t: typeof defaultI18n.t;
}) {
  const theme = useTheme(), comparison = profile && sourceChoiceComparison(profile);
  const choice = comparison?.changedFields.length === 0 ? comparison.name : undefined;
  const facts = [
    {label: t('next.remoteSources.saved'), help: source.name},
    ...(profile ? [{label: t('next.remoteSources.summaryLimits', {connections: profile.values.connections, requests: profile.values.requestsPerSecond})}] : []),
    ...(choice ? [{label: t('next.remoteSources.summaryTreatment', {preset: sourceReadPresetLabel(t, choice)})}] : []),
    library ? {label: t('next.remoteSources.summaryLibrary', {library}), help: t(count === 1 ? 'next.remoteSources.summaryLibraryOne' : 'next.remoteSources.summaryLibraryMany', {count, source: source.name})}
      : {label: t('next.remoteSources.summaryNoLibrary'), help: t('next.remoteSources.summaryNoLibraryHelp')},
  ];
  return <View style={{gap: 20}}>
    <View style={{alignItems: 'center', paddingVertical: 20, gap: 12}}><View style={{padding: 16, borderRadius: 32, backgroundColor: alpha(theme.colors.healthy, .12)}}><Icon name="i-check" size={30} color={theme.colors.healthy}/></View><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{source.name}</Text></View>
    <View style={{gap: 20}}>{facts.map((fact, index) => <View key={index} style={{flexDirection: 'row', gap: 12}}><Icon name="i-check" size={20} color={theme.colors.healthy}/><View style={{flex: 1, gap: 4}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{fact.label}</Text>{fact.help && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{fact.help}</Text>}</View></View>)}</View>
  </View>;
}

/** Saving a source without assigning folders is an explicit, complete outcome. */
export function SourceCompletion({source, controller, t, isCurrent, onCancel, onOpen, onAddAnother}: {
  source: RemoteSource; controller: NativeRemoteSourcesController; t: typeof defaultI18n.t; isCurrent: () => boolean;
  onCancel: () => void; onOpen: () => void; onAddAnother: () => void;
}) {
  const current = useAttempt(isCurrent), [profile, setProfile] = useState<SourceReadSettings>(), [error, setError] = useState<string>(), [retry, setRetry] = useState(0);
  useEffect(() => {
    setError(undefined); setProfile(undefined);
    void controller.readProfile(source, current).then(value => {if (current()) setProfile(value);}, problem => {if (current()) setError(remoteSourceError(t, problem));});
  }, [controller, source, current, t, retry]);
  return <Page title={source.name} t={t} busy={false} onCancel={onCancel} wizardStep={5}
    footer={<View style={{gap: 12}}><Button onPress={onOpen}>{t('next.remoteSources.openSource', {name: source.name})}</Button><Button variant="glass" onPress={onAddAnother}>{t('next.remoteSources.addAnother')}</Button></View>}>
    <SourceCompletionFacts source={source} profile={profile} t={t}/>
    <Problem message={error}/>
    {error && <Button variant="glass" onPress={() => setRetry(value => value + 1)}>{t('next.remoteSources.retry')}</Button>}
  </Page>;
}
