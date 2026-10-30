import type {ReactNode} from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeShellRoutes, type NativeShellRouteRegistry} from '../../shell/route-registry';
import {createNativeWatchModule, type NativeWatchModuleBindings, type NativeWatchEntryMenu} from './module';

const entryMenus = new WeakMap<NativeShellBootstrapContext, NativeWatchEntryMenu>();

/** Read the existing registered module; this never creates a second Watch host. */
export function nativeWatchEntryMenu(context: NativeShellBootstrapContext): NativeWatchEntryMenu | undefined {
  return context.current() ? entryMenus.get(context) : undefined;
}

/** One registered factory identity deliberately serves both outlets: bind()
 * then creates one authority-local module/control clock and disposes it once. */
export function registerNativeWatch(createBindings: (context: NativeShellBootstrapContext) => NativeWatchModuleBindings,
  registry: NativeShellRouteRegistry = nativeShellRoutes): () => void {
  const create = (context: NativeShellBootstrapContext) => {
    const module = createNativeWatchModule(context, createBindings(context));
    entryMenus.set(context, module.entryMenu);
    function render(runtime: NativeShellRuntime): ReactNode;
    function render(route: NativeShellRoute, runtime: NativeShellRuntime): ReactNode;
    function render(value: NativeShellRoute | NativeShellRuntime, runtime?: NativeShellRuntime): ReactNode {
      return 'kind' in value ? runtime ? value.kind === 'channel' ? module.channel.render(value) : module.title.render(value, runtime) : null : module.render(value);
    }
    return Object.freeze({render, renderOverlays: module.renderOverlays, back: module.back, dispose: () => {
      if (entryMenus.get(context) === module.entryMenu) entryMenus.delete(context);
      module.dispose();
    }});
  };
  const offTitle = registry.registerRoute('title', create);
  let offChannel: (() => void) | undefined, offWatch: (() => void) | undefined;
  try {offChannel = registry.registerRoute('channel', create); offWatch = registry.registerWatch(create);}
  catch (error) {offChannel?.(); offTitle(); throw error;}
  return () => {offWatch?.(); offChannel?.(); offTitle();};
}
