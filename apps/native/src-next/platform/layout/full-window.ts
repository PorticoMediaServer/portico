import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

/** The one presentation request a player may make of the host: turn this window sideways.
 * Native owns the actual host window/scene; JS owns which fence and route asked for it. */
export type NativeFullWindowOrientation = Readonly<{
  /** True only when this build's own module reports real OS support for the request. */
  supported: true;
  /** A fresh id per opening from the app's native UUID source; never reused across instances. */
  owner: () => string;
  /** Resolves false when the request was not issued (another owner, no live host); rejects only
   * when the host call itself failed, so callers can report an honest failure. */
  enter: (owner: string) => Promise<boolean>;
  /** Restores only this exact owner; a late owner never releases a successor. */
  leave: (owner: string) => void;
  /** A refusal after a request was issued (iOS refused the geometry). */
  subscribeFailure: (listener: (owner: string, error: string) => void) => () => void;
}>;

type Bridge = Readonly<{
  orientationSupported?: boolean;
  enter?: (owner: string) => Promise<boolean>;
  leave?: (owner: string) => void;
}>;

/** Televisions have no orientation of their own and never register the module. */
const bridge = Platform.isTV ? undefined : (NativeModules.PorticoFullWindow as Bridge | undefined);
/** The app's own native UUID source, read at call time: the module-load snapshot may predate it. */
const runtime = NativeModules.PorticoRuntime as undefined | {playbackRequestId?: () => string};
const requestId = typeof runtime?.playbackRequestId === 'function' ? runtime.playbackRequestId.bind(runtime) : undefined;

export const nativeFullWindowOrientation: NativeFullWindowOrientation | undefined =
  bridge?.orientationSupported === true && typeof bridge.enter === 'function' && typeof bridge.leave === 'function' && !!requestId
    ? Object.freeze({
        supported: true as const,
        owner: () => requestId!(),
        enter: (owner: string) => bridge.enter!(owner),
        leave: (owner: string) => {try {bridge.leave!(owner);} catch {}},
        subscribeFailure: (listener: (owner: string, error: string) => void) => {
          const subscription = new NativeEventEmitter(NativeModules.PorticoFullWindow).addListener('portico.fullwindow',
            (event: Readonly<{owner?: unknown; error?: unknown}>) => {
              if (typeof event?.owner === 'string') listener(event.owner, typeof event.error === 'string' ? event.error : 'orientation_refused');
            });
          return () => subscription.remove();
        },
      })
    : undefined;
