import type {createOwnerSources} from '../../../live/owner/OwnerSourcesPage';
import type {OwnerLiveSource} from '../../../live/owner/source-controller';
import {Tuners} from './Tuners';
import {RecordingPermissions} from './RecordingPermissions';
import {RecordingRules} from './RecordingRules';
import {RecordingDefaults} from './RecordingDefaults';
import {RecordingFolder} from './RecordingFolder';
import {RecordingStorage} from './RecordingStorage';

type Source = ReturnType<typeof createOwnerSources>;
export function RecordingPage({source, sources}: {source: Source; sources: readonly OwnerLiveSource[]}) {
  if (!source.current()) return null;
  return <>
    <Tuners controller={source.tuners}/>
    <RecordingPermissions controller={source.permissions} embedded/>
    <RecordingRules controller={source.rules} permissions={source.permissions} sources={sources}/>
    <RecordingDefaults form={source.recording} current={source.current}/>
    <RecordingFolder controller={source.root} api={source.api}/>
    <RecordingStorage controller={source.storage}/>
  </>;
}
export function RecordingAdvanced({source}: {source: Source}) {
  if (!source.current()) return null;
  return <>
    <RecordingDefaults form={source.recording} current={source.current} advanced/>
    <RecordingStorage controller={source.storage} advanced/>
  </>;
}
