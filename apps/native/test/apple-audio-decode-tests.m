#import <Foundation/Foundation.h>
#import "../Native/PorticoAudioRender.h"
#import "../Native/PorticoServerTrust.h"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengeCancelAuthenticationChallenge,nil);}
@end
@interface NSObject (PorticoDecodeTest)
+ (NSDictionary *)conformance:(NSString *)directory;
@end
int main(int argc,const char **argv){@autoreleasepool{
 if(argc!=2)return 2;
 NSDictionary *result=[NSClassFromString(@"PorticoFileRender") conformance:[NSString stringWithUTF8String:argv[1]]];
 BOOL ok=[result[@"fixtures"]count]>0;
 for(NSDictionary *fixture in result[@"fixtures"])ok=ok&&[fixture[@"status"]isEqual:@"pass"];
 puts([[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted|NSJSONWritingSortedKeys error:nil]encoding:NSUTF8StringEncoding].UTF8String);
 return ok?0:1;
}}
