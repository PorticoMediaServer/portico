export type CertificateConfig = Readonly<{enabled:boolean; publicAddress:string; publicPort:number; revision:string}>;
export type CertificateStatus = Readonly<{
 configured:boolean; authorityId:string; config:CertificateConfig; state:string; errorCode?:string;
 namespace?:string; dnsName?:string; issuer?:string; environment:'production'|'staging'; orderState?:string;
 notAfter?:string; renewAt?:string; nextAttemptAt?:string; tlsReady:boolean; publiclyTrusted:boolean;
 listenerBound:boolean; listenPort:number; routeHostname?:string; routeUrl?:string; routeErrorCode?:string; reachability:'probe_required';
}>;
