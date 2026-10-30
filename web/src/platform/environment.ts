export type WebEnvironment = Readonly<{VITE_HOSTED_WEB?: string; VITE_ACCOUNT_WORKSPACE?: string; VITE_HOSTED_URL?: string; VITE_APP_VERSION?: string; VITE_BUILD_ID?: string; VITE_CAST_APPLICATION_ID?: string}>;
export function webEnvironment(env: WebEnvironment, page: {hostname: string; origin: string}) {
  return {
    hostedWeb: env.VITE_HOSTED_WEB === 'true' || page.hostname === 'web.getportico.tv',
    accountWorkspace: env.VITE_ACCOUNT_WORKSPACE === 'true' || page.hostname === 'web.getportico.tv',
    hostedUrl: env.VITE_HOSTED_URL,
    appVersion: env.VITE_APP_VERSION ?? '',
    buildId: env.VITE_BUILD_ID ?? '',
    castApplicationId: env.VITE_CAST_APPLICATION_ID,
  };
}
export const currentWebEnvironment = () => webEnvironment(import.meta.env, location);
