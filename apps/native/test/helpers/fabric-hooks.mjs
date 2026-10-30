import {fabricHost, React} from './fabric-host.mjs';
import {afterEach} from 'node:test';

// Mount a hook probe as a real React function component. The probe may capture
// elements for separate presentation assertions, but its descendants are not
// mounted. Use fabricHost directly for host identity, refs and reconciliation.
// No dispatcher, state, subscription or effect implementation lives here.
const mounted = new Set();
export function createFabricHookHost(body) {
  let host, active = false;
  const Probe = () => {body(); return null;};
  return {
    async mount() {
      if (active) throw new Error('Fabric hook probe already mounted');
      host = await fabricHost();
      active = true;
      mounted.add(host);
      try {await host.render(React.createElement(Probe));}
      catch (error) {
        active = false;
        mounted.delete(host);
        try {await host.unmount();} catch {}
        throw error;
      }
    },
    async rerender() {
      if (!active) throw new Error('Fabric hook probe not mounted');
      await host.render(React.createElement(Probe));
    },
    async unmount() {
      if (!active) return;
      active = false;
      mounted.delete(host);
      await host.unmount();
    },
    async flush() {if (active) await host.settle();},
  };
}

afterEach(async () => {
  for (const host of [...mounted]) {
    mounted.delete(host);
    await host.unmount();
  }
});

export async function act(work) {
  await work();
  await new Promise(resolve => setImmediate(resolve));
  // All probes can participate in a provider/publication interaction. Settle
  // each on the actual scheduler, including work scheduled on an earlier root
  // by a later root's effects.
  for (let pass = 0; pass < 2; pass++) {
    for (const host of mounted) await host.settle();
  }
}
