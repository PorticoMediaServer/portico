/** Anything that can send one request to the selected server, as the Troubleshooting sources use it. */
export type ReportTransport={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
