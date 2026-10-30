#import <Foundation/Foundation.h>

/* The original payload and every font/IDX companion stay within the current media
 * grant and selected resource. This helper performs no fetch or authorization. */
static NSURL *PorticoScopedSubtitleURL(id value,NSString *source,NSString *resource) {
 if(![value isKindOfClass:NSString.class]||[value length]>4096)return nil;
 NSURL *url=[NSURL URLWithString:value],*media=[NSURL URLWithString:source];NSArray *parts=media.path.pathComponents;
 if(parts.count<4||!url.host.length||![@[@"http",@"https"] containsObject:url.scheme.lowercaseString]||![url.scheme.lowercaseString isEqual:media.scheme.lowercaseString]||![url.host.lowercaseString isEqual:media.host.lowercaseString]||![(url.port?:@([url.scheme isEqual:@"https"]?443:80)) isEqual:(media.port?:@([media.scheme isEqual:@"https"]?443:80))]||url.user||url.password||url.query||url.fragment)return nil;
 NSString *prefix=[NSString stringWithFormat:@"/v1/media/subtitles/%@/",parts[3]];
 if(![url.path hasPrefix:prefix])return nil;
 NSString *tail=[url.path substringFromIndex:prefix.length];
 NSRegularExpression *pattern=[NSRegularExpression regularExpressionWithPattern:@"^[A-Za-z0-9_-]{1,128}/[1-9][0-9]*(?:/original\\.(?:ass|ssa|sup|idx|sub|mks)|/fonts/[0-9]{1,2})$" options:0 error:nil];
 if(![pattern firstMatchInString:tail options:0 range:NSMakeRange(0,tail.length)])return nil;
 if(resource.length&&![url.path hasPrefix:[resource stringByAppendingString:@"/"]])return nil;return url;
}

/* IDX requires the actual supplied SUB address; a filename substitution is not a
 * producer fact. Validate it before either half is fetched. */
static NSURL *PorticoVobSubCompanionURL(NSDictionary *render,NSString *source,NSURL *index) {
 if(!index||![index.pathExtension.lowercaseString isEqual:@"idx"])return nil;
 NSURL *pair=PorticoScopedSubtitleURL(render[@"companionUrl"],source,[index.path stringByDeletingLastPathComponent]);
 return [pair.pathExtension.lowercaseString isEqual:@"sub"]?pair:nil;
}
