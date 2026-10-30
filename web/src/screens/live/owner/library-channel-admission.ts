import {subscribeFacts} from '../../../shell/fact-source';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes.ts';
import type {LibraryChannel} from '@core/library-channels.ts';
import type {ShellAssemblyContext} from '../../../shell/bootstrap';
import type {OwnerSources} from './OwnerSourcesPage';
import {libraryChannelUpload} from './library-channel-upload';

/** Prepared composition for the real page export. Creating these ports does
 * not mount a tab or start a read. The existing owner lifetime owns admission. */
export function libraryChannelAdmission(context: ShellAssemblyContext, owner: OwnerSources) {
  if (!owner.current() || !context.isCurrent() || owner.serverId !== context.session().viewer.serverId) throw {code: 'cancelled'};
  return {
    api: owner.api,
    serverId: owner.serverId,
    isCurrent: owner.current,
    reportError: owner.reportError,
    get libraries() {return context.facts.identity.libraries;},
    subscribe: (listener: () => void) => subscribeFacts(context, listener),
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    logo: (channel: LibraryChannel, revision?: number) => owner.current() && context.isCurrent() ? context.artwork.channelLogo(channel.config.id, revision) : undefined,
    sampleArt: (id: string) => owner.current() && context.isCurrent() ? context.artwork.foreground(encodedRoutePath('get_items_id_art_kind', {id: encodeURIComponent(id), kind: 'poster'}), 80, 2 / 3) : undefined,
    uploadLogo: libraryChannelUpload(context, owner.current, owner.reportError),
  };
}
