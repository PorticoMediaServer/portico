export type ClaimExpected={operationId:string;localGeneration:string;revision:string};
export type ClaimApproval={operationId:string;serverId:string;publicKey:string;localGeneration:string;name:string;expectedRevision:string;purpose:"setup"|"switch"|"change-owner";currentOwnerAccountId:string;previousOwnerRole:string;returnRegistrationId:string};
export type ClaimStatus={state:string;identity:{serverId:string;localGeneration:string};operation?:ClaimExpected;accountId?:string;installationAcknowledged:boolean;approvalRequired:boolean;actions:string[];approvalRequest?:ClaimApproval};
