import type {RegisteredSignInContext} from './registered-sign-in-context.ts';

export type ReturnPurpose='setup'|'switch'|'change-owner'|'sign-in';
export type ReturnPending=Readonly<{serverId:string;purpose:ReturnPurpose;claimOperationId:string;registrationId:string;state:string;verifier:string;serverChallenge:string;installationId:string;createdAt:number;registrationOrigin?:string;moveTicket?:string;moveCode?:string}>;
export type SameTabReturnStart=Omit<ReturnPending,'verifier'|'createdAt'|'registrationOrigin'|'moveTicket'|'moveCode'>&{codeChallenge:string};
export type RegisteredSignIn=Readonly<{start:SameTabReturnStart;registration:{id:string;origin:string};hostedOrigin:string;expiresAt:string}>;
export type RegisteredSignInWithContext=RegisteredSignIn&Readonly<{context:RegisteredSignInContext}>;
