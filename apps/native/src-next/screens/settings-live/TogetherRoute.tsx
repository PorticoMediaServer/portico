import React from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {NativeScreenLifetime} from './lifetime';
import {NativeTogetherController} from './together-controller';
import {NativeTogetherMedia} from './together-media';
import {nativeTogetherSession} from './together-session';
import {NativeTogetherScreen} from './TogetherScreen';
import {nativeTogetherPages} from './together-page';

export function createNativeTogetherRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const controller = new NativeTogetherController(lifetime, nativeTogetherSession(context));
  const media = new NativeTogetherMedia(lifetime, context.api);
  const pages = nativeTogetherPages(context,lifetime);
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'together') throw new Error('native_together_route_mismatch');
      const page=pages.capture(route);if(!page)return null;
      return <NativeTogetherScreen key={page.key} page={page} ports={{context}} controller={controller} media={media} runtime={runtime} onBack={page.back}/>;
    },
    dispose: () => {pages.dispose();controller.dispose(); media.dispose(); lifetime.dispose();},
  };
}

nativeShellRoutes.registerRoute('together', createNativeTogetherRenderer);
