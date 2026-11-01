import {routePath as developmentRoutePath} from "../../contracts/src/development/routes.ts";
import {parseDirectSignIn,type DirectSignIn,type ProfileAPI} from './profile-management.ts';

// Development only. A native bundle reaches this module only when it was built
// with PORTICO_DEV_SIGN_IN=1; a release bundle never contains it.

/** The server's seeded e2e owner, if it has one (`GET /v1/dev/e2e`; 404 otherwise). */
export async function devE2EUsername(api:ProfileAPI,signal?:AbortSignal):Promise<string|undefined>{
 try{const raw=await api.request<unknown>(developmentRoutePath("get_dev_e2e"),'GET',undefined,signal);return !!raw&&typeof raw==='object'&&typeof (raw as {username?:unknown}).username==='string'&&/^[\x20-\x7e]{1,64}$/.test((raw as {username:string}).username)?(raw as {username:string}).username:undefined;}catch{return undefined;}
}
/** Signs in as the seeded e2e owner without a password (`POST /v1/dev/e2e/sign-in`, loopback servers built with devtrust). */
export async function devE2ESignIn(api:ProfileAPI,username:string,signal?:AbortSignal):Promise<DirectSignIn>{
 return parseDirectSignIn(await api.request<unknown>(developmentRoutePath("post_dev_e2e_sign_in"),'POST',{username},signal));
}
