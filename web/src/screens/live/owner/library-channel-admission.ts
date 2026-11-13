import {ownerItemArtworkPath} from '@core/owner-upload-requests.ts';
import {subscribeFacts} from '../../../shell/fact-source';
import type {LibraryChannel} from '@core/server-admin/owner-library-channels.ts';
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
    logo: (channel: LibraryChannel) => owner.current() && context.isCurrent() && channel.logo ? context.artwork.foreground(channel.logo, 44, 1) : undefined,
    sampleArt: (id: string) => owner.current() && context.isCurrent() ? context.artwork.foreground(ownerItemArtworkPath(encodeURIComponent(id), 'poster'), 80, 2 / 3) : undefined,
    uploadLogo: libraryChannelUpload(context, owner.current, owner.reportError),
  };
}
