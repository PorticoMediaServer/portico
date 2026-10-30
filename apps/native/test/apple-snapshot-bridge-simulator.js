// Real React host/run-loop counterpart of the owned UIKit snapshot suite. No server/account.
const React = require('react');
const {AppRegistry, NativeModules, NativeEventEmitter, requireNativeComponent, findNodeHandle, View} = require('react-native');
const Player = requireNativeComponent('PorticoPlayer');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function waitFor(ready) {
  const deadline = Date.now() + 12000;
  while (!ready() && Date.now() < deadline) await delay(20);
  if (!ready()) throw Error('player did not reach requested source time');
}
function emit(value) { NativeModules.PorticoRuntime.recordTestEvent(JSON.stringify(value)); }
function Probe() {
  const ref = React.useRef(null);
  const [lease, setLease] = React.useState(null);
  const [missing, setMissing] = React.useState(false);
  React.useEffect(() => {
    const engine = NativeModules.PorticoPlaybackModule, snapshots = NativeModules.PorticoVideoSnapshot;
    let position = 0, held = null, ownedLease = null;
    const assertions = [];
    const check = (passed, name) => { assertions.push({name, passed}); if (!passed) throw Error(name); };
    const subscription = new NativeEventEmitter(engine).addListener('PorticoPlaybackFact', fact => {
      if (typeof fact.position === 'number') position = fact.position;
    });
    (async () => {
      try {
        const acquired = await engine.acquire('owned-snapshot-bridge');
        ownedLease = acquired.lease;
        setLease(ownedLease);
        await waitFor(() => ref.current && Number.isSafeInteger(findNodeHandle(ref.current)));
        const tag = findNodeHandle(ref.current);
        check(Number.isSafeInteger(tag) && tag > 0, 'real player view tag');
        const binding = {lease: ownedLease, intentId: 1, generation: 1, itemId: 'authored-video', sessionId: 'snapshot-probe'};
        const source = {reactTag: tag, engine: 'native', lease: ownedLease, binding: {...binding}};
        delete source.binding.lease;
        await engine.command({binding, commandRevision: 1, source: NativeModules.PorticoRuntime.testLogin.snapshotUrl, paused: false, rate: 1});
        await waitFor(() => position > .25);
        held = 'react-red';
        const red = await snapshots.capture(held, source);
        check(red.width > 0 && red.height > 0, 'real red-period frame');
        emit({snapshotImage: {name: 'red', image: red}});
        setMissing(true);
        await waitFor(() => position > 5.25);
        await snapshots.release(held);
        held = 'react-blue';
        const blue = await snapshots.capture(held, source);
        check(blue.width > 0 && blue.height > 0, 'fresh zero-bounds frame in real React run loop');
        emit({snapshotImage: {name: 'blue', image: blue}});
        const afterCapture = position;
        setMissing(false);
        await waitFor(() => position > afterCapture + .5);
        check(findNodeHandle(ref.current) === tag, 'restored same native player view advances');
        emit({snapshotRestored: true, position});
        // The Mac host records actual visible pixels, separately from generated JPEGs.
        await delay(2000);
        emit({appleBridgeResult: {assertions, failures: 0, position, scope: 'real React snapshot bridge with zero bounds'}});
        // The host copies the authored frame before terminating this disposable app.
      } catch (error) {
        emit({appleBridgeResult: {assertions, failures: 1, error: String(error), position}});
      }
    })();
    return () => { subscription.remove(); if (held) snapshots.release(held); if (ownedLease) engine.release(ownedLease); };
  }, []);
  return React.createElement(View, {style: {flex: 1}, onLayout: () => NativeModules.PorticoRuntime.bootDrawn()},
    lease && React.createElement(Player, {ref, lease, style: {width: missing ? 0 : 320, height: missing ? 0 : 180}}));
}
AppRegistry.registerComponent('Portico', () => Probe);
