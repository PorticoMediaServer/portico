#import <Foundation/Foundation.h>
#import <React/RCTHTTPRequestHandler.h>
#import "../Native/PorticoServerTrust.h"
#import <TargetConditionals.h>
#if !TARGET_OS_SIMULATOR
#error These tests must run in a disposable, isolated simulator device set.
#endif
static BOOL expect(BOOL value,const char *what){if(!value)fprintf(stderr,"FAIL %s\n",what);return value;}
static BOOL waitFor(BOOL (^ready)(void)){
 NSDate *until=[NSDate dateWithTimeIntervalSinceNow:12];
 while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];
 return ready();
}
static BOOL proof(NSString *origin,NSString *fingerprint,NSString *body,NSDictionary **answer){
 __block BOOL done=NO;__block NSDictionary *result=nil;
 [PorticoServerTrust prove:origin fingerprint:fingerprint body:body completion:^(NSDictionary *value){result=value;done=YES;}];
 BOOL completed=waitFor(^BOOL{return done;});
 if(answer)*answer=result;
 return completed;
}
static BOOL rejectedProof(NSString *origin,NSString *fingerprint,NSString *body){
 NSDictionary *answer=nil;
 return proof(origin,fingerprint,body,&answer)&&answer==nil;
}
int main(int argc,const char **argv){@autoreleasepool{
 if(![NSProcessInfo.processInfo.environment[@"PORTICO_APPLE_ISOLATED_SIMULATOR_TESTS"] isEqual:@"1"]||argc<4)return 2;
 NSString *origin=[NSString stringWithUTF8String:argv[1]],*fingerprint=[NSString stringWithUTF8String:argv[2]],*phase=[NSString stringWithUTF8String:argv[3]];
 NSString *a=@"srv_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",*b=@"srv_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB";
 BOOL ok=YES;
 if([phase isEqual:@"readback"]){
  ok=expect(![PorticoServerTrust pinnedAt:origin]&&![PorticoServerTrust pinnedAt:@"https://other-route.invalid"]&&![PorticoServerTrust pinnedAt:@"https://named-route.invalid"]&&[[PorticoServerTrust pinnedAt:@"https://keep.invalid"][@"serverId"] isEqual:b],"second-process durable Forget readback")&&ok;
  printf("{\"durableForgetReadbackPass\":%s}\n",ok?"true":"false");return ok?0:1;
 }
 NSUserDefaults *defaults=[[NSUserDefaults alloc]initWithSuiteName:@"group.tv.getportico.portico"];
 [defaults removeObjectForKey:@"portico.certificates.v1"];[defaults synchronize];
 ok=expect(rejectedProof(origin,fingerprint,@"{}"),"no proof before an actual refused connection")&&ok;
 __block BOOL firstDone=NO;__block NSError *firstError=nil;
 [[[PorticoServerTrust session] dataTaskWithURL:[NSURL URLWithString:[origin stringByAppendingString:@"/initial"]] completionHandler:^(NSData *data,NSURLResponse *response,NSError *error){firstError=error;firstDone=YES;}]resume];
 ok=expect(waitFor(^BOOL{return firstDone;})&&firstError&&[[PorticoServerTrust refusedAt:origin] isEqual:fingerprint],"real TLS refusal records observed leaf")&&ok;
 NSDictionary *result=nil;BOOL completed=proof(origin,fingerprint,@"{}",&result);
 ok=expect(completed&&[result[@"status"] isEqual:@200]&&[result[@"body"] isEqual:@"{\"proof\":true}"]&&[result[@"tlsLeafFingerprint"] isEqual:fingerprint],"same-response actual handshake leaf returned")&&ok;
 ok=expect(rejectedProof(origin,@"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",@"{}"),"wrong refused fingerprint cannot exchange proof")&&ok;
 ok=expect(rejectedProof([origin stringByAppendingString:@"/v1/system"],fingerprint,@"{}"),"no path/generic request expansion")&&ok;
 ok=expect(rejectedProof(origin,fingerprint,@"{\"mode\":\"redirect\"}"),"redirect refused")&&ok;
 ok=expect(rejectedProof(origin,fingerprint,@"{\"mode\":\"oversize\"}"),"unknown-length response bounded while receiving")&&ok;
 ok=expect([PorticoServerTrust pin:origin fingerprint:fingerprint serverId:a],"pin observed leaf")&&ok;
 __block BOOL pinnedDone=NO;__block NSError *pinnedError=nil;
 [[[PorticoServerTrust session] dataTaskWithURL:[NSURL URLWithString:[origin stringByAppendingString:@"/pinned"]] completionHandler:^(NSData *data,NSURLResponse *response,NSError *error){pinnedError=error;pinnedDone=YES;}]resume];
 ok=expect(waitFor(^BOOL{return pinnedDone;})&&!pinnedError&&[[PorticoServerTrust refusedAt:origin] isEqual:fingerprint],"saved pin succeeds without clearing system-trust refusal observation")&&ok;
 ok=expect([PorticoServerTrust pin:@"https://other-route.invalid" fingerprint:fingerprint serverId:a]&&[PorticoServerTrust pin:@"https://named-route.invalid" fingerprint:fingerprint serverId:b]&&[PorticoServerTrust pin:@"https://keep.invalid" fingerprint:fingerprint serverId:b],"write isolated multiple ID/origin pins")&&ok;
 ok=expect(![PorticoServerTrust forgetServerCertificates:a origins:@[@"https://named-route.invalid",@"https://bad.invalid/path"]]&&[PorticoServerTrust pinnedAt:origin]&&[PorticoServerTrust pinnedAt:@"https://named-route.invalid"],"invalid Forget set changes nothing")&&ok;
 ok=expect([PorticoServerTrust forgetServerCertificates:a origins:@[@"https://named-route.invalid"]]&&![PorticoServerTrust pinnedAt:origin]&&![PorticoServerTrust pinnedAt:@"https://other-route.invalid"]&&![PorticoServerTrust pinnedAt:@"https://named-route.invalid"]&&[PorticoServerTrust pinnedAt:@"https://keep.invalid"],"offline atomic Forget removes every ID and named origin only")&&ok;
 printf("{\"actualTLSProofAndForgetPass\":%s}\n",ok?"true":"false");return ok?0:1;
}}
