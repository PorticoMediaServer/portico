import {createFabricHookHost} from './fabric-hooks.mjs';

// A screen's own hooks run on Fabric; returned element descriptions are
// inspected by the caller. This does not mount the screen's child components.
export async function sceneHost(body) {
  const probe = createFabricHookHost(body);
  await probe.mount();
  return {update: () => probe.rerender(), unmount: () => probe.unmount(),
    host: {settle: () => probe.flush()},
    async act(work) {await work(); await probe.flush();}};
}
