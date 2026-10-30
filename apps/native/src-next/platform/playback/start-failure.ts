/** The held first-start machine is shared with web in client-core. The native
 * runtime supplies its HTTP, events, visibility and original-caller ports. */
export {startFailureHttp} from '../../../../../packages/client-core/src/playback-v1/held-start.ts';
export type {HeldPlaybackStartFailure as NativePlaybackStartFailure} from '../../../../../packages/client-core/src/playback-v1/held-start.ts';
