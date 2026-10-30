import {encodedRoutePath} from '../../../../../../../packages/contracts/src/routes';
import type {LibraryChannel} from '../../../../../../../packages/client-core/src/library-channels';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeChannelsGroup} from './model';
/** A fixed foreground logo representation in the existing native artwork queue.
 * Only a checked upload receipt supplies a revision; config revisions are unrelated. */
export function nativeChannelLogoArtwork(group:NativeChannelsGroup,runtime:NativeShellRuntime,channel:LibraryChannel){
  if(!group.current()||!group.reader.getSnapshot().channels?.includes(channel)||!/^[A-Za-z0-9_-]{1,128}$/.test(channel.config.id))return;
  const revision=group.logos.revision(channel.config.id);
  if(revision!==undefined&&(!Number.isSafeInteger(revision)||revision<1))return;
  const path=encodedRoutePath('get_library_channels_id_logo',{id:encodeURIComponent(channel.config.id)})+(revision===undefined?'':'?v='+revision);
  return runtime.artwork(path);
}
