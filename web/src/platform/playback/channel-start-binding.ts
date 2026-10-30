import type {ChannelPlayback, ChannelStartReceipt} from '@core/playback-v1/channel-control.ts';
import type {ChannelReference} from '@core/playback/linear-protocol.ts';
import type {WebStartRegistration} from './preparing-start.ts';

/** A per-stack view of the existing one channel control and its ordered stop
 * barrier. The receipt is issued only inside the canonical pre-I/O hook; no
 * observer can stand in for SDK819's synchronous pre-install registration. */
export function ownedWebChannels(control: ChannelPlayback, registration: WebStartRegistration | undefined,
  captureCurrent: (channel: ChannelReference) => () => boolean): ChannelPlayback {
  const bound: ChannelPlayback = {
    open(channel, sink, options = {}) {
      if (options.captureStart) throw new Error('channel_start_owner_already_supplied');
      const current = captureCurrent(channel);
      return control.open(channel, sink, {...options, captureStart: original => {
        if (registration) return registration.beginChannel(original, current);
        // No Shell receipt owner means no cleanup authority. Playback still
        // uses its actual canonical admission; late resources are refused,
        // never downgraded to the legacy raw DELETE-by-id path.
        const isCurrent = () => original.isCurrent() && current();
        const receipt: ChannelStartReceipt = Object.freeze({isCurrent, capturePreparing: () => undefined,
          claimInstall: () => isCurrent(), retireUninstalled: () => {}, finish: () => {}});
        return receipt;
      }});
    },
    recover: () => control.recover(), pause: () => control.pause(), resume: () => control.resume(),
    seek: seconds => control.seek(seconds), goLive: () => control.goLive(), retry: () => control.retry(),
    observe: (seconds, seekId, errorCode) => control.observe(seconds, seekId, errorCode), close: () => control.close(),
  };
  return Object.freeze(bound);
}
