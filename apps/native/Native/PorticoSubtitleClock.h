#import <Foundation/Foundation.h>
#import <math.h>

/** Original ASS/bitmap file offsets are complete source-relative corrections. An
 * explicit unsupported correction is an error, never permission to use the plan. */
static NSNumber *PorticoSubtitleOverlayOffsetUS(NSDictionary *command) {
 NSDictionary *render=[command[@"render"] isKindOfClass:NSDictionary.class]?command[@"render"]:nil;
 id file=render[@"offsetMs"];
 if(file){
  if(![file isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)file)==CFBooleanGetTypeID())return nil;
  double milliseconds=[file doubleValue];
  if(!isfinite(milliseconds)||milliseconds < -86400000||milliseconds > 86400000)return nil;
  return @(milliseconds*1000);
 }
 id raw=command[@"offsetUs"];
 double plan=[raw isKindOfClass:NSString.class]?[raw doubleValue]:0;
 return isfinite(plan)?@(plan):nil;
}
