// RestoredShell's session lifecycle, settled at "signed out" so a test's own restore path runs
// unchanged. Tests of the lifecycle itself use the real modules.
export function restoredShellLifecycleStub() {
  const state = Object.freeze({kind: 'signedOut'});
  const lifecycle = {
    start() {}, resume() {}, report() {},
    subscribe: () => () => {}, getSnapshot: () => ({generation: 0, state}),
  };
  const ports = {lendInstallation() {}, adopt() { return undefined; }};
  return {
    '../platform/native-session-lifecycle': {
      nativeSessionLifecycle: () => ({lifecycle, ports}),
      lifecycleSettled: async () => state,
      choosingProfile: value => value.kind === 'choosingProfile' || value.kind === 'enteringPin',
    },
    '../platform/session-lifecycle-native': {
      NativeProfileGated: class NativeProfileGated extends Error {},
      nativeProfileGated: error => error?.code === 'profile_pin_reentry',
    },
    '../screens/accounts/LifecycleProfileGate': {LifecycleProfileGate: () => null},
  };
}
