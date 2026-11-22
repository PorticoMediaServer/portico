import React from 'react';
import type {NativeShellBootstrapContext} from './bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from './route-registry';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {NativeTogetherController} from '../screens/settings-live/together-controller';
import {NativeTogetherMedia} from '../screens/settings-live/together-media';
import {nativeTogetherSession} from '../screens/settings-live/together-session';
import {TogetherPage} from '../pages/touch/together/TogetherPage';
import {TogetherPage as TVTogetherPage} from '../pages/tv/together/TogetherPage';
import {Platform} from 'react-native';
import {nativeTogetherPages} from '../screens/settings-live/together-page';

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
      const Page = Platform.isTV ? TVTogetherPage : TogetherPage;
      return <Page key={page.key} page={page} ports={{context}} controller={controller} media={media} runtime={runtime} onBack={page.back} joinCode={route.joinCode}/>;
    },
    dispose: () => {pages.dispose();controller.dispose(); media.dispose(); lifetime.dispose();},
  };
}

nativeShellRoutes.registerRoute('together', createNativeTogetherRenderer);
