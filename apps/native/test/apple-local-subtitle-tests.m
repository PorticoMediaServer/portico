#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoSubtitleSelection.h"
#import "../Native/PorticoSubtitleOverlay.h"
#import "../Native/PorticoLocalSubtitle.h"
#import "../Native/PorticoServerTrust.h"
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengePerformDefaultHandling,nil);}
@end
#pragma clang diagnostic pop
static int assertions,failures;
static void check(BOOL value,const char *label){assertions++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",label);}}
static BOOL waitFor(BOOL (^ready)(void)){NSDate *end=[NSDate dateWithTimeIntervalSinceNow:5];while(!ready()&&end.timeIntervalSinceNow>0)[NSRunLoop.mainRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];return ready();}
static void drain(void){[NSRunLoop.mainRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.15]];}
int main(int argc,char **argv){@autoreleasepool{
 if(argc!=3)return 2;
 NSFileManager *files=NSFileManager.defaultManager;NSURL *root=[NSURL fileURLWithPath:NSProcessInfo.processInfo.environment[@"TMPDIR"] isDirectory:YES];
 NSURL *directory=[root URLByAppendingPathComponent:NSUUID.UUID.UUIDString isDirectory:YES];[files createDirectoryAtURL:directory withIntermediateDirectories:YES attributes:nil error:nil];
 NSURL *media=[directory URLByAppendingPathComponent:@"media.mp4"],*vtt=[directory URLByAppendingPathComponent:@"caption.vtt"],*other=[root URLByAppendingPathComponent:@"outside.vtt"];
 [@"media" writeToURL:media atomically:YES encoding:NSUTF8StringEncoding error:nil];
 NSString *text=@"WEBVTT\n\n00:02.000 --> 00:04.000\nAlready baked\n\n";[text writeToURL:vtt atomically:YES encoding:NSUTF8StringEncoding error:nil];[text writeToURL:other atomically:YES encoding:NSUTF8StringEncoding error:nil];
 BOOL (^allowed)(NSURL *)=^BOOL(NSURL *url){return url.isFileURL&&[url.path hasPrefix:root.path];};
 check(PorticoLocalSubtitleURL(vtt.absoluteString,media.absoluteString,allowed)!=nil,"same download resource admitted");
 check(!PorticoLocalSubtitleURL(other.absoluteString,media.absoluteString,allowed),"another download resource refused");
 NSURL *link=[directory URLByAppendingPathComponent:@"link.vtt"];[files createSymbolicLinkAtURL:link withDestinationURL:other error:nil];
 check(!PorticoLocalSubtitleURL(link.absoluteString,media.absoluteString,allowed),"symlink escape refused");
 check(!PorticoLocalSubtitleURL([vtt.absoluteString stringByAppendingString:@"?secret=1"],media.absoluteString,allowed),"query refused");
 check(!PorticoLocalSubtitleURL(vtt.absoluteString,@"https://media.test/video",allowed),"online media cannot read local captions");
 check(!PorticoReadLocalSubtitle(vtt,media.absoluteString,3,allowed),"read byte cap enforced");
 check([PorticoReadLocalSubtitle(vtt,media.absoluteString,4096,allowed)length]>0,"bounded opened-file read succeeds");
 PorticoSubtitleSelection *selection=[PorticoSubtitleSelection new];selection.allowsLocalFile=allowed;
 __block NSString *cue=@"",*status=@"pending";selection.onText=^(NSDictionary *value){cue=value[@"text"]?:@"";};selection.onStatus=^(NSString *value){status=value;};
 NSDictionary *binding=@{@"intentId":@1,@"sessionId":@"local",@"generation":@1};
 NSMutableDictionary *command=[@{@"mode":@"track",@"revision":@1,@"resourceRevision":@1,@"binding":binding,@"resourceId":@"caption",@"documentUrl":vtt.absoluteString,@"offsetUs":@"1000000",@"localSessionOffsetUs":@"0"}mutableCopy];
 [selection apply:command item:nil source:media.absoluteString];check(waitFor(^BOOL{return ![status isEqual:@"pending"];}),"local VTT read completed");
 [selection tick:CMTimeMake(2500000,1000000)];check([cue isEqual:@"Already baked"],"zero session offset preserves baked VTT timing");
 command[@"revision"]=@2;command[@"localSessionOffsetUs"]=@"2000000";[selection apply:command item:nil source:media.absoluteString];[selection tick:CMTimeMake(2500000,1000000)];check(!cue.length,"positive session offset delays already-baked cue");
 [selection tick:CMTimeMake(4500000,1000000)];check([cue isEqual:@"Already baked"],"positive session adjustment applied once");
 command[@"revision"]=@3;command[@"localSessionOffsetUs"]=@"-1000000";[selection apply:command item:nil source:media.absoluteString];[selection tick:CMTimeMake(1500000,1000000)];check([cue isEqual:@"Already baked"],"negative adjustment advances baked cue");
 command[@"revision"]=@2;command[@"localSessionOffsetUs"]=@"2000000";[selection apply:command item:nil source:media.absoluteString];[selection tick:CMTimeMake(1500000,1000000)];check([cue isEqual:@"Already baked"],"stale offset command cannot replace newer adjustment");
 command[@"revision"]=@4;command[@"localSessionOffsetUs"]=@"0";[selection apply:command item:nil source:media.absoluteString];[selection tick:CMTimeMake(1500000,1000000)];check(!cue.length,"reset restores baked timeline");
 [selection apply:@{@"mode":@"off"} item:nil source:media.absoluteString];check(!selection.overlayActive&&!cue.length,"Off retires local reader");
 command[@"localSessionOffsetUs"]=@"600000001";[selection apply:command item:nil source:media.absoluteString];check([status isEqual:@"subtitle_command_invalid"]&&!selection.overlayActive,"out-of-range session delta refused");command[@"localSessionOffsetUs"]=@"0";
 command[@"revision"]=@5;[selection apply:command item:nil source:media.absoluteString];[selection apply:@{@"mode":@"off"} item:nil source:media.absoluteString];drain();[selection tick:CMTimeMake(2500000,1000000)];check(!cue.length&&!selection.overlayActive,"stale asynchronous local read cannot restore Off captions");
 command[@"revision"]=@6;status=@"pending";[selection apply:command item:nil source:media.absoluteString];check(waitFor(^BOOL{return ![status isEqual:@"pending"];}),"reopen starts fresh read");[selection tick:CMTimeMake(2500000,1000000)];check([cue isEqual:@"Already baked"],"reopen uses zero session correction");[selection invalidate];
 // The real rich renderer must load an admitted original and reject foreign font/companion
 // resources before fetching or decoding them. No URLSession is involved in local reads.
 NSURL *ass=[directory URLByAppendingPathComponent:@"caption.ass"];
 NSString *script=@"[Script Info]\nScriptType: v4.00+\nPlayResX: 320\nPlayResY: 180\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,24,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,Rich local\n";
 [script writeToURL:ass atomically:YES encoding:NSUTF8StringEncoding error:nil];
 PorticoSubtitleOverlay *overlay=[PorticoSubtitleOverlay new];overlay.allowsLocalFile=allowed;__block NSString *richStatus=@"pending";__block BOOL image=NO;overlay.onStatus=^(NSString *value){richStatus=value;};overlay.onImage=^(UIImage *value){if(value)image=YES;};
 NSMutableDictionary *rich=[@{@"mode":@"track",@"offsetUs":@"0",@"render":@{@"kind":@"ass",@"url":ass.absoluteString}}mutableCopy];
 [overlay apply:rich source:media.absoluteString];check(waitFor(^BOOL{return ![richStatus isEqual:@"pending"];}),"local ASS load completed");check(!richStatus.length&&overlay.active,"real rich decoder accepts admitted original");
 [overlay renderAtSourceTime:2 size:CGSizeMake(320,180)];check(waitFor(^BOOL{return image;}),"real rich renderer produces local subtitle pixels");
 rich[@"render"]=@{@"kind":@"ass",@"url":ass.absoluteString,@"fonts":@[@{@"name":@"foreign",@"url":other.absoluteString}]};richStatus=@"pending";[overlay apply:rich source:media.absoluteString];check(waitFor(^BOOL{return ![richStatus isEqual:@"pending"];}),"foreign font decision completed");check([richStatus isEqual:@"subtitle_font_invalid"],"foreign local font is refused");
 NSURL *validFont=[directory URLByAppendingPathComponent:@"Manrope.ttf"];[files copyItemAtURL:[NSURL fileURLWithPath:@(argv[2])] toURL:validFont error:nil];
 rich[@"render"]=@{@"kind":@"ass",@"url":ass.absoluteString,@"fonts":@[@{@"name":@"Manrope.ttf",@"url":validFont.absoluteString}]};richStatus=@"pending";[overlay apply:rich source:media.absoluteString];check(waitFor(^BOOL{return ![richStatus isEqual:@"pending"];}),"admitted font read completed");check(!richStatus.length,"real attached local font passes CoreText validation and decode");
 NSURL *font=[directory URLByAppendingPathComponent:@"invalid.ttf"];[@"not a font" writeToURL:font atomically:YES encoding:NSUTF8StringEncoding error:nil];rich[@"render"]=@{@"kind":@"ass",@"url":ass.absoluteString,@"fonts":@[@{@"name":@"broken",@"url":font.absoluteString}]};richStatus=@"pending";[overlay apply:rich source:media.absoluteString];check(waitFor(^BOOL{return ![richStatus isEqual:@"pending"];}),"damaged font decision completed");check([richStatus isEqual:@"subtitle_font_invalid"],"damaged font fails honestly");
 NSURL *idx=[directory URLByAppendingPathComponent:@"captions.idx"];[@"# VobSub index file, v7\n" writeToURL:idx atomically:YES encoding:NSUTF8StringEncoding error:nil];
 rich[@"render"]=@{@"kind":@"vobsub",@"url":idx.absoluteString,@"companionUrl":other.absoluteString};[overlay apply:rich source:media.absoluteString];check([richStatus isEqual:@"subtitle_source_invalid"]&&!overlay.active,"VobSub requires an admitted matching SUB companion before loading");
 NSURL *pair=[directory URLByAppendingPathComponent:@"captions.sub"];
 [files removeItemAtURL:idx error:nil];[files copyItemAtURL:[NSURL fileURLWithPath:[@(argv[1]) stringByAppendingPathComponent:@"test.idx"]] toURL:idx error:nil];[files copyItemAtURL:[NSURL fileURLWithPath:[@(argv[1]) stringByAppendingPathComponent:@"test.sub"]] toURL:pair error:nil];
 rich[@"render"]=@{@"kind":@"vobsub",@"url":idx.absoluteString,@"companionUrl":pair.absoluteString};richStatus=@"pending";[overlay apply:rich source:media.absoluteString];check(waitFor(^BOOL{return ![richStatus isEqual:@"pending"];}),"admitted VobSub pair read completed");check(!richStatus.length,"real rich decoder accepts both local VobSub files");image=NO;[overlay renderAtSourceTime:1.2 size:CGSizeMake(320,180)];check(waitFor(^BOOL{return image;}),"real local VobSub produces bitmap pixels");
 [overlay invalidate];rich[@"render"]=@{@"kind":@"ass",@"url":ass.absoluteString};[overlay apply:rich source:media.absoluteString];[overlay invalidate];richStatus=@"retired";image=NO;drain();check([richStatus isEqual:@"retired"]&&!image&&!overlay.active,"retired rich load cannot restore captions");
 [overlay invalidate];[files removeItemAtURL:directory error:nil];[files removeItemAtURL:other error:nil];printf("{\"assertions\":%d,\"failures\":%d}\n",assertions,failures);return failures?1:0;
}}
