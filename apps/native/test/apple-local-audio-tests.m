#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoAudioSelection.h"
#if PORTICO_LOCAL_CONTROLLER_TEST
#import "../Native/PorticoPlaybackController.h"
#import "../Native/PorticoServerTrust.h"
// The fixture reads only admitted local files; no trust/HTTP path is under test.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (id<AVAssetResourceLoaderDelegate>)resourceLoaderDelegate {return nil;}
+ (dispatch_queue_t)resourceLoaderQueue {return dispatch_get_main_queue();}
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengePerformDefaultHandling,nil);}
@end
#pragma clang diagnostic pop
#endif
static int assertions,failures;
static void check(BOOL value,const char *label){assertions++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",label);}}
static BOOL waitFor(BOOL (^ready)(void)){NSDate *end=[NSDate dateWithTimeIntervalSinceNow:8];while(!ready()&&end.timeIntervalSinceNow>0)[NSRunLoop.mainRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];return ready();}
int main(int argc,char **argv){@autoreleasepool{
 if(argc!=2)return 2;
 AVPlayerItem *item=[AVPlayerItem playerItemWithURL:[NSURL fileURLWithPath:@(argv[1])]];AVPlayer *player=[AVPlayer playerWithPlayerItem:item];
 PorticoLocalAudioSelection *selection=[PorticoLocalAudioSelection new];NSMutableArray<NSDictionary *> *facts=[NSMutableArray new];
 selection.onFact=^(NSDictionary *fact){[facts addObject:fact];};[selection apply:nil item:item];
 check(waitFor(^BOOL{return [facts.lastObject[@"audioTracks"]count]==2;}),"actual local AVPlayer exposes two playable options with duplicate metadata");
 NSDictionary *inventory=facts.lastObject;NSArray *tracks=inventory[@"audioTracks"];
 check(tracks.count==2&&![tracks[0][@"id"]isEqual:tracks[1][@"id"]],"opaque identities distinguish real options, independent of title/language");
 if(tracks.count!=2)return 1;
 NSString *target=[tracks[0][@"selected"]boolValue]?tracks[1][@"id"]:tracks[0][@"id"];
 NSDictionary *command=@{@"revision":@1,@"catalogRevision":inventory[@"audioTrackCatalogRevision"],@"trackId":target};[selection apply:command item:item];
 check(waitFor(^BOOL{for(NSDictionary *fact in facts)if([fact[@"type"]isEqual:@"localAudioTrackApplied"])return YES;return NO;}),"selection acknowledged from AVPlayer's actual selected option");
 NSDictionary *applied=facts.lastObject;check([applied[@"trackId"]isEqual:target]&&[applied[@"audioTrackRevision"]isEqual:@1],"acknowledgement preserves command and catalogue identity");
 AVMediaSelectionGroup *group=[item.asset mediaSelectionGroupForMediaCharacteristic:AVMediaCharacteristicAudible];
 check([item.currentMediaSelection selectedMediaOptionInMediaSelectionGroup:group]!=nil,"AVPlayer selection really exists");
 NSUInteger count=facts.count;[selection apply:command item:item];check(facts.count==count,"duplicate command is not reapplied or acknowledged twice");
 [selection apply:@{@"revision":@2,@"catalogRevision":@9999,@"trackId":target} item:item];check([facts.lastObject[@"type"]isEqual:@"localAudioTrackFailed"],"wrong catalogue fails explicitly");
 [selection apply:@{@"revision":@3,@"catalogRevision":inventory[@"audioTrackCatalogRevision"],@"trackId":@"missing"} item:item];check([facts.lastObject[@"type"]isEqual:@"localAudioTrackFailed"],"missing native option fails explicitly");
 [selection invalidate];count=facts.count;[NSNotificationCenter.defaultCenter postNotificationName:AVPlayerItemMediaSelectionDidChangeNotification object:item];check(facts.count==count,"retired item notification cannot publish");
 AVPlayerItem *replacement=[AVPlayerItem playerItemWithURL:[NSURL fileURLWithPath:@(argv[1])]];[player replaceCurrentItemWithPlayerItem:replacement];[selection apply:nil item:replacement];
 check(waitFor(^BOOL{return facts.count>count&&[facts.lastObject[@"type"]isEqual:@"mediaInfo"];}),"replacement loads its own actual catalogue");
 check(![facts.lastObject[@"audioTrackCatalogRevision"]isEqual:inventory[@"audioTrackCatalogRevision"]],"replacement retires old catalogue identity");
 [selection apply:command item:replacement];check([facts.lastObject[@"type"]isEqual:@"localAudioTrackFailed"],"old catalogue cannot select a replacement item");
 // A queued inventory load is fenced too, even before its completion reaches the main queue.
 [selection invalidate];[selection apply:nil item:item];[selection invalidate];count=facts.count;[NSRunLoop.mainRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.2]];check(facts.count==count,"invalidated asynchronous load stays retired");
 [player replaceCurrentItemWithPlayerItem:nil];[selection invalidate];
#if PORTICO_LOCAL_CONTROLLER_TEST
 NSFileManager *files=NSFileManager.defaultManager;NSURL *directory=[[[files URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject URLByAppendingPathComponent:@"Downloads" isDirectory:YES] URLByAppendingPathComponent:NSUUID.UUID.UUIDString isDirectory:YES];
 [files createDirectoryAtURL:directory withIntermediateDirectories:YES attributes:nil error:nil];NSURL *local=[directory URLByAppendingPathComponent:@"media.mp4"];[files copyItemAtURL:[NSURL fileURLWithPath:@(argv[1])] toURL:local error:nil];
 PorticoPlaybackController *controller=[PorticoPlaybackController new];NSMutableArray<NSDictionary *> *bridgeFacts=[NSMutableArray new];controller.onFact=^(NSDictionary *fact){[bridgeFacts addObject:fact];};
 NSDictionary *lease=[controller acquire:@"local-track-test"],*binding=@{@"lease":lease[@"lease"],@"intentId":@1,@"sessionId":@"local",@"generation":@1,@"itemId":@"authored"};
 NSMutableDictionary *bridgeCommand=[@{@"binding":binding,@"commandRevision":@1,@"source":local.absoluteString,@"rate":@1,@"paused":@YES}mutableCopy];
 check(![controller acceptCommand:bridgeCommand],"public controller accepts admitted download");
 check(waitFor(^BOOL{for(NSDictionary *fact in bridgeFacts)if([fact[@"audioTracks"]count]==2)return YES;return NO;}),"public controller publishes actual local inventory");
 NSDictionary *bridgeInventory=nil;for(NSDictionary *fact in bridgeFacts)if([fact[@"audioTracks"]count]==2)bridgeInventory=fact;
 if(bridgeInventory){
  NSString *choice=[bridgeInventory[@"audioTracks"][0][@"selected"]boolValue]?bridgeInventory[@"audioTracks"][1][@"id"]:bridgeInventory[@"audioTracks"][0][@"id"];
  bridgeCommand[@"commandRevision"]=@2;bridgeCommand[@"localAudioTrack"]=@{@"revision":@1,@"catalogRevision":bridgeInventory[@"audioTrackCatalogRevision"],@"trackId":choice};
  check(![controller acceptCommand:bridgeCommand],"public controller accepts exactly bound local selection");
  check(waitFor(^BOOL{for(NSDictionary *fact in bridgeFacts)if([fact[@"type"]isEqual:@"localAudioTrackApplied"])return YES;return NO;}),"public controller forwards acknowledged native choice");
  NSDictionary *ack=nil;for(NSDictionary *fact in bridgeFacts)if([fact[@"type"]isEqual:@"localAudioTrackApplied"])ack=fact;
  check([ack[@"binding"]isEqual:binding]&&[ack[@"trackId"]isEqual:choice],"bridge acknowledgement carries exact playback binding");
  bridgeCommand[@"commandRevision"]=@3;bridgeCommand[@"localAudioTrack"]=@{@"revision":@YES,@"catalogRevision":@1,@"trackId":choice};
  check([controller acceptCommand:bridgeCommand]!=nil,"malformed local command refused before state mutation");
 }
 NSURL *fallback=[directory URLByAppendingPathComponent:@"fallback.vtt"],*original=[directory URLByAppendingPathComponent:@"original.ass"],*font=[directory URLByAppendingPathComponent:@"broken.ttf"];
 [@"WEBVTT\n\n00:00.000 --> 00:10.000\nSaved fallback\n" writeToURL:fallback atomically:YES encoding:NSUTF8StringEncoding error:nil];[@"original" writeToURL:original atomically:YES encoding:NSUTF8StringEncoding error:nil];[@"broken" writeToURL:font atomically:YES encoding:NSUTF8StringEncoding error:nil];
 [bridgeCommand removeObjectForKey:@"localAudioTrack"];bridgeCommand[@"commandRevision"]=@4;bridgeCommand[@"subtitles"]=@{@"binding":@{@"intentId":@1,@"sessionId":@"local",@"generation":@1},@"mode":@"track",@"revision":@1,@"resourceRevision":@1,@"resourceId":@"fallback",@"offsetUs":@"0",@"localSessionOffsetUs":@"0",@"documentUrl":fallback.absoluteString,@"render":@{@"kind":@"ass",@"url":original.absoluteString,@"fonts":@[@{@"name":@"broken.ttf",@"url":font.absoluteString}]}};
 check(![controller acceptCommand:bridgeCommand],"controller accepts rich original with saved VTT fallback");
 check(waitFor(^BOOL{return [controller.subtitleText isEqual:@"Saved fallback"];}),"asynchronous rich/font failure falls back to admitted VTT without failing media");
 [controller invalidate];[files removeItemAtURL:directory error:nil];
#endif
 printf("{\"assertions\":%d,\"failures\":%d}\n",assertions,failures);return failures?1:0;
}}
