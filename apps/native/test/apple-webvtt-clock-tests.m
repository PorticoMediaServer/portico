#import <Foundation/Foundation.h>
#import "../Native/PorticoSubtitleSelection.h"
#import "../Native/PorticoServerTrust.h"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengeCancelAuthenticationChallenge,nil);}
@end
static BOOL expect(BOOL value,const char *what){if(!value)fprintf(stderr,"FAIL %s\n",what);return value;}
int main(void){@autoreleasepool{
 NSFileManager *files=NSFileManager.defaultManager;NSURL *root=[[NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES]URLByAppendingPathComponent:NSUUID.UUID.UUIDString isDirectory:YES];[files createDirectoryAtURL:root withIntermediateDirectories:YES attributes:nil error:nil];NSURL *vtt=[root URLByAppendingPathComponent:@"subtitles.vtt"],*media=[root URLByAppendingPathComponent:@"media.mp4"];
 [@"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nAuthored fixture cue\n" writeToURL:vtt atomically:YES encoding:NSUTF8StringEncoding error:nil];
 PorticoSubtitleSelection *selection=[PorticoSubtitleSelection new];__block BOOL loaded=NO;__block NSString *text=@"";
 selection.allowsLocalFile=^BOOL(NSURL *url){return [url.path hasPrefix:[root.path stringByAppendingString:@"/"]];};
 selection.onStatus=^(NSString *code){loaded=code.length==0;};selection.onText=^(NSDictionary *cue){text=cue[@"text"]?:@"";};
 NSDictionary *command=@{@"mode":@"track",@"revision":@1,@"resourceRevision":@1,@"resourceId":@"fixture",@"offsetUs":@"1000000",@"documentUrl":vtt.absoluteString,@"binding":@{@"lease":@"fixture"}};
 [selection apply:command item:nil source:media.absoluteString];NSDate *until=[NSDate dateWithTimeIntervalSinceNow:5];while(!loaded&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];
 BOOL ok=expect(loaded,"actual local WebVTT parsed")&&loaded;
 [selection tick:CMTimeMakeWithSeconds(.2,1000)];ok=expect(!text.length,"no early cue")&&ok;
 [selection tick:CMTimeMakeWithSeconds(1.2,1000)];ok=expect([text isEqual:@"Authored fixture cue"],"WebVTT correction is not applied a second time")&&ok;
 [selection tick:CMTimeMakeWithSeconds(2.2,1000)];ok=expect(!text.length,"cue clears at baked end")&&ok;
 [selection tick:CMTimeMakeWithSeconds(1.2,1000)];ok=expect([text isEqual:@"Authored fixture cue"],"backward seek uses baked timing")&&ok;
 [selection invalidate];[files removeItemAtURL:root error:nil];printf("{\"webvttBakedOffsetOncePass\":%s}\n",ok?"true":"false");return ok?0:1;
}}
