import type {WebPlaybackAuthority, WebListeningLifetime} from '../../../platform/playback';
import type {WatchPlaybackContext} from '../play-on-binding';

/** The supported management LIST. It does not select or open a library record. */
export const lyricsSettingsDestination = Object.freeze({to: '/settings' as const,
  search: Object.freeze({scope: 'server' as const, section: 'libraries', row: 'libraries.list'})});
export type ListeningSettingsInput = Readonly<{
  context: WatchPlaybackContext;
  navigate(destination: typeof lyricsSettingsDestination): unknown;
}>;
export type ListeningSettingsCommand = WebListeningLifetime & Readonly<{
  /** Original host receipt only; no external predicate or mutable latest owner. */
  active(): boolean;
}>;
export type ListeningLibrarySettings = Readonly<{
  libraryId: string;
  isCurrent(): boolean;
  open(command: ListeningSettingsCommand): Promise<void>;
}>;

/** A layout-owned offer from the original real shell/Settings capability, never
 * lyric resource ownership or canShare. Missing facts mean no offered action. */
export function captureLibrarySettings(input: ListeningSettingsInput | undefined, authority: WebPlaybackAuthority, libraryId: string, host:WebListeningLifetime): ListeningLibrarySettings | undefined {
  if (!input || !libraryId) return;
  try {
    const originalAuthority=Object.freeze({...authority}),hostSignal=host.signal,hostCurrent=host.isCurrent.bind(host);
    const context = input.context, session = context.session.bind(context), isCurrent = context.isCurrent.bind(context), navigate = input.navigate.bind(input);
    const facts = context.facts, identity = facts?.identity, epoch = context.epoch;
    if (!identity || identity.viewer.role!=='owner' || identity.primaryProfile!==true || identity.capabilities?.manageServer!==true) return;
    const capabilities=identity.capabilities,identityViewer=identity.viewer,libraries=identity.libraries;
    const original = session(),familyId=original.sessionFamilyId,deviceId=original.deviceId, viewer = Object.freeze({...original.viewer});
    if([familyId,viewer.serverId,viewer.accountId,viewer.profileId].some(value=>typeof value!=='string'||!value))return;
    const library = libraries.find(row => row.id === libraryId);
    const matches = (value: ReturnType<typeof session>) => {
      const v = value.viewer;
      return value.sessionFamilyId === familyId && value.deviceId === deviceId
        && v.role === 'owner' && v.serverId === viewer.serverId && v.authority === viewer.authority && v.accountId === viewer.accountId && v.profileId === viewer.profileId;
    };
    const factsMatch = () => context.facts === facts && context.facts?.identity === identity && context.epoch === epoch
      && epoch === originalAuthority.epoch && identity.primaryProfile === true && identity.capabilities===capabilities && capabilities.manageServer === true && identity.viewer===identityViewer
      && identity.viewer.role === 'owner' && identity.viewer.serverId === viewer.serverId && identity.viewer.authority === viewer.authority
      && identity.viewer.accountId === viewer.accountId && identity.viewer.profileId === viewer.profileId
      && viewer.role === 'owner' && viewer.serverId === originalAuthority.serverId && viewer.authority === originalAuthority.authority
      && viewer.accountId === originalAuthority.accountId && viewer.profileId === originalAuthority.profileId
      && !!library && identity.libraries===libraries && libraries.includes(library) && library.id === libraryId;
    let retired = false;
    const current = () => {
      if (retired || hostSignal.aborted) return false;
      let accepted = false;
      try {accepted = hostCurrent() && !hostSignal.aborted && factsMatch() && matches(session()) && isCurrent() && !hostSignal.aborted && hostCurrent() && !hostSignal.aborted && matches(session()) && isCurrent() && !hostSignal.aborted && hostCurrent() && !hostSignal.aborted && factsMatch();} catch {/* Refused original shell. */}
      if (!accepted) retired = true;
      return accepted && !retired && !hostSignal.aborted;
    };
    if (!current()) return;
    return Object.freeze({libraryId, isCurrent:current, open: async (command: ListeningSettingsCommand) => {
      const signal = command.signal, active = command.active.bind(command), admitted = command.isCurrent.bind(command);
      const local = () => !signal.aborted && !hostSignal.aborted && !retired && active();
      if (!local() || !current() || !local() || !admitted() || !local() || !current() || !local() || !admitted() || !local() || !matches(session()) || !local() || !isCurrent() || !local() || !hostCurrent() || !local() || !factsMatch() || !local())
        throw new DOMException('Library settings opening retired', 'AbortError');
      await navigate(lyricsSettingsDestination);
    }});
  } catch {return;} // Missing or throwing original session/facts never grants Settings.
}
