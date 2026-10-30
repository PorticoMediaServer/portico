#import "../Native/PorticoSubtitlePayload.h"

static NSUInteger checks;
static void check(BOOL ok,const char *message){checks++;if(!ok){fprintf(stderr,"FAIL %s\n",message);exit(1);}}
int main(void){@autoreleasepool{
 NSString *source=@"https://media.example:32500/v1/media/grant/hls/master.m3u8";
 NSString *root=@"https://media.example:32500/v1/media/subtitles/grant/resource/3/";
 NSURL *index=PorticoScopedSubtitleURL([root stringByAppendingString:@"original.idx"],source,nil);
 NSString *sub=[root stringByAppendingString:@"original.sub"];
 NSURL *pair=PorticoVobSubCompanionURL(@{@"companionUrl":sub},source,index);
 check([pair.absoluteString isEqual:sub],"the exact supplied SUB URL is preserved");
 check(!PorticoVobSubCompanionURL(@{},source,index),"missing companion is never synthesized from IDX");
 for(id missing in @[@"",NSNull.null,@42])check(!PorticoVobSubCompanionURL(@{@"companionUrl":missing},source,index),"malformed companion rejected before fetching");
 for(NSString *address in @[
  @"https://other.example:32500/v1/media/subtitles/grant/resource/3/original.sub",
  @"http://media.example:32500/v1/media/subtitles/grant/resource/3/original.sub",
  @"https://media.example:32501/v1/media/subtitles/grant/resource/3/original.sub",
  @"https://user:pass@media.example:32500/v1/media/subtitles/grant/resource/3/original.sub",
  @"https://media.example:32500/v1/media/subtitles/grant/resource/3/original.sub?token=other",
  @"https://media.example:32500/v1/media/subtitles/grant/resource/3/original.sub#fragment",
  @"https://media.example:32500/v1/media/subtitles/other/resource/3/original.sub",
  @"https://media.example:32500/v1/media/subtitles/grant/other/3/original.sub",
  @"https://media.example:32500/v1/media/subtitles/grant/resource/4/original.sub",
  @"https://media.example:32500/v1/media/subtitles/grant/resource/3/original.ass",
  @"https://media.example:32500/v1/media/subtitles/grant/resource/3/fonts/0",
 ])check(!PorticoVobSubCompanionURL(@{@"companionUrl":address},source,index),"companion retains origin, grant, resource, revision and SUB format boundaries");
 check(!PorticoVobSubCompanionURL(@{@"companionUrl":sub},source,[NSURL URLWithString:[root stringByAppendingString:@"original.sup"]]),"only a genuine IDX can pair a SUB");
 NSString *resource=[index.path stringByDeletingLastPathComponent];
 check(PorticoScopedSubtitleURL([root stringByAppendingString:@"fonts/0"],source,resource)!=nil,"font fetch keeps the same resource scope");
 check(!PorticoScopedSubtitleURL(@"https://media.example:32500/v1/media/subtitles/grant/other/3/fonts/0",source,resource),"font cannot cross selected resource");
 for(NSString *format in @[@"ass",@"ssa",@"sup",@"idx",@"sub",@"mks"])
  check(PorticoScopedSubtitleURL([root stringByAppendingFormat:@"original.%@",format],source,nil)!=nil,"canonical original payload routes remain accepted");
 NSString *defaultSource=@"https://media.example/v1/media/grant/video.mp4";
 NSURL *defaultIndex=PorticoScopedSubtitleURL(@"https://media.example:443/v1/media/subtitles/grant/resource/3/original.idx",defaultSource,nil);
 check(PorticoVobSubCompanionURL(@{@"companionUrl":@"https://media.example/v1/media/subtitles/grant/resource/3/original.sub"},defaultSource,defaultIndex)!=nil,"implicit and explicit HTTPS default ports identify the same origin");
 printf("Apple subtitle payload scope: %lu checks passed\n",(unsigned long)checks);
 return 0;
}}
