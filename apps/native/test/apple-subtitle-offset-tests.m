#import "../Native/PorticoSubtitleClock.h"

int main(void){@autoreleasepool{
 NSUInteger checks=0;
#define CHECK(value) do { checks++; if(!(value)){fprintf(stderr,"Subtitle offset check %lu failed at line %d\n",(unsigned long)checks,__LINE__);return 1;} } while(0)
 CHECK([PorticoSubtitleOverlayOffsetUS(@{@"offsetUs":@"250000",@"render":@{}}) isEqual:@250000]);
 CHECK([PorticoSubtitleOverlayOffsetUS(@{@"offsetUs":@"-250000",@"render":@{}}) isEqual:@-250000]);
 CHECK([PorticoSubtitleOverlayOffsetUS(@{}) isEqual:@0]);
 for(NSNumber *file in @[@0,@-0.5,@0.5,@250,@-250,@86400000,@-86400000]){
  NSDictionary *command=@{@"offsetUs":@"250000",@"render":@{@"timeDomain":@"source-relative",@"offsetMs":file}};
  CHECK([PorticoSubtitleOverlayOffsetUS(command) isEqual:@(file.doubleValue*1000)]);
 }
 // Unsupported explicit values cannot silently replace the complete file correction
 // with the unrelated, otherwise valid plan correction.
 for(id file in @[@86400000.5,@-86400000.5,@(DBL_MAX),@(-DBL_MAX),@(NAN),@(INFINITY),@(-INFINITY),@YES,@"0",NSNull.null]){
  CHECK(PorticoSubtitleOverlayOffsetUS(@{@"offsetUs":@"250000",@"render":@{@"offsetMs":file}})==nil);
 }
 // A file correction identical to the plan is still applied exactly once.
 CHECK([PorticoSubtitleOverlayOffsetUS(@{@"offsetUs":@"250000",@"render":@{@"offsetMs":@250}}) isEqual:@250000]);
 printf("Apple complete subtitle offset: %lu checks passed\n",(unsigned long)checks);
 return 0;
}}
