#ifndef PorticoListeningPolicy_h
#define PorticoListeningPolicy_h
#include <stdbool.h>
#include <math.h>
typedef struct { double lease; double horizon; bool valid; } PorticoListeningLease;
/** Request-start anchoring intentionally subtracts all transport latency. */
static inline PorticoListeningLease PorticoListeningLeaseAt(double start,double server,double expiry,double horizon,bool leaseValid) {
 if(!isfinite(start)||!isfinite(server)||!isfinite(expiry)||!isfinite(horizon)||start<0)return (PorticoListeningLease){0,0,false};
 return (PorticoListeningLease){leaseValid?start+fmax(0,fmin(expiry,horizon)-server):0,start+fmax(0,horizon-server),true};
}
static inline bool PorticoListeningMayPlay(bool owner,bool terminal,bool interrupted,bool paused,bool ended,bool media,double now,double lease,double horizon) {
 return owner&&!terminal&&!interrupted&&!paused&&!ended&&media&&isfinite(now)&&now<lease&&now<horizon;
}
static inline bool PorticoListeningTimerExpired(double wallMS,double mono,double deadlineMS,double monoLimit) {
 return isfinite(deadlineMS)&&deadlineMS>0&&(wallMS>=deadlineMS||mono>=monoLimit);
}
/** The old long-pause trigger selects no hidden amount. Return only the
 * positive displacement of the chosen profile amount, clamped at the start. */
static inline double PorticoAudiobookResumeDisplacement(double elapsed,double position,double choiceMS) {
 if(!isfinite(elapsed)||elapsed<300||!isfinite(position)||position<=0||
   !(choiceMS==0||choiceMS==5000||choiceMS==15000||choiceMS==30000))return 0;
 return fmin(position,choiceMS/1000);
}
#ifdef __OBJC__
#import <Foundation/Foundation.h>
static inline BOOL PorticoPolicyNumber(id value,double minimum,double maximum) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&isfinite([value doubleValue])&&[value doubleValue]>=minimum&&[value doubleValue]<=maximum;
}
static inline BOOL PorticoPolicyBool(id value) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)==CFBooleanGetTypeID();
}
static inline BOOL PorticoBookRate(id value) {
 return PorticoPolicyNumber(value,.5,3)&&[@[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2,@2.5,@3] containsObject:value];
}
/** The canonical native policy projects only these two listening preferences. */
static inline BOOL PorticoListeningPreferencesValid(id value) {
 if(value==NSNull.null)return YES;
 if(![value isKindOfClass:NSDictionary.class]||[value count]!=2||!PorticoBookRate(value[@"bookRate"])||!PorticoPolicyNumber(value[@"passoutMinutes"],0,120)||![@[@0,@30,@60,@90,@120] containsObject:value[@"passoutMinutes"]])return NO;
 return YES;
}
/** This is a separate private bridge projection; the server's closed listening
 * preference response still contains only bookRate and passoutMinutes. */
static inline BOOL PorticoAudiobookPreferencesValid(id value) {
 if(!value)return YES;
 if(![value isKindOfClass:NSDictionary.class]||[value count]!=3)return NO;
 return PorticoPolicyNumber(value[@"skipBackMs"],10000,60000)&&[@[@10000,@15000,@30000,@60000] containsObject:value[@"skipBackMs"]]
  &&PorticoPolicyNumber(value[@"skipForwardMs"],15000,90000)&&[@[@15000,@30000,@60000,@90000] containsObject:value[@"skipForwardMs"]]
  &&PorticoPolicyNumber(value[@"resumeRewindMs"],0,30000)&&[@[@0,@5000,@15000,@30000] containsObject:value[@"resumeRewindMs"]];
}
static inline BOOL PorticoListeningEffectsValid(id value) {
 if(![value isKindOfClass:NSDictionary.class]||!PorticoPolicyBool(value[@"gapless"])||!PorticoPolicyNumber(value[@"crossfadeSeconds"],0,12)||![@[@"off",@"track",@"album"] containsObject:value[@"normalization"]])return NO;
 for(NSString *key in value)if(![@[@"gapless",@"crossfadeSeconds",@"normalization",@"neverBoost",@"smoothTransitions",@"sampleRateMatching"] containsObject:key])return NO;
 for(NSString *key in @[@"neverBoost",@"smoothTransitions"])if(value[key]&&!PorticoPolicyBool(value[key]))return NO;
 if(value[@"sampleRateMatching"]&&![@[@"smart",@"strict",@"off"] containsObject:value[@"sampleRateMatching"]])return NO;
 return YES;
}
static inline BOOL PorticoListeningRateAllowed(NSString *kind,id value) {
 return [kind isEqual:@"audiobook_file"]?PorticoBookRate(value):PorticoPolicyNumber(value,1,1);
}
static inline NSNumber *PorticoPreferredListeningRate(NSString *kind,id preferences) {
 if(![kind isEqual:@"audiobook_file"])return @1;
 return [preferences isKindOfClass:NSDictionary.class]&&PorticoBookRate(preferences[@"bookRate"])?preferences[@"bookRate"]:nil;
}
static inline BOOL PorticoPolicyWireID(id value) {
 return [value isKindOfClass:NSString.class]&&[value length]>=1&&[value length]<=128&&[value rangeOfCharacterFromSet:[[NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"] invertedSet]].location==NSNotFound;
}
/** A book part's chapter steps for the lock screen's Previous and Next (MU-7): the shape
 * client-core's bookChapterSteps sends, `{sessionId, itemId, steps, before, after}`. */
static inline BOOL PorticoChapterStepsValid(id value) {
 if(![value isKindOfClass:NSDictionary.class]||[value count]!=5||!PorticoPolicyWireID(value[@"sessionId"])||!PorticoPolicyWireID(value[@"itemId"])
   ||![@[@"end",@"queue"] containsObject:value[@"before"]]||![@[@"end",@"queue"] containsObject:value[@"after"]])return NO;
 NSArray *steps=value[@"steps"];
 if(![steps isKindOfClass:NSArray.class]||!steps.count||steps.count>4100)return NO;
 int run=0; // 0 before the part's own steps, 1 in them, 2 after them
 for(NSDictionary *step in steps){
  if(![step isKindOfClass:NSDictionary.class]||step.count!=2||!PorticoPolicyNumber(step[@"startSeconds"],0,1e9))return NO;
  id entry=step[@"entryId"];
  if(entry==NSNull.null){if(run==2)return NO;run=1;}
  else if(!PorticoPolicyWireID(entry))return NO;
  else if(run==1)run=2;
 }
 return run>0;
}
static inline id PorticoChapterStepEntry(NSArray *steps,NSInteger at){id entry=((NSDictionary *)steps[at])[@"entryId"];return entry==NSNull.null?nil:entry;}
static inline double PorticoChapterStepStart(NSArray *steps,NSInteger at){return [((NSDictionary *)steps[at])[@"startSeconds"] doubleValue];}
static inline NSDictionary *PorticoChapterGo(NSArray *steps,NSInteger at,id past) {
 if(at<0||at>=(NSInteger)steps.count)return [past isEqual:@"queue"]?@{@"kind":@"queue"}:nil;
 NSString *entry=PorticoChapterStepEntry(steps,at);
 return entry?@{@"kind":@"entry",@"entryId":entry,@"seconds":@(PorticoChapterStepStart(steps,at))}:@{@"kind":@"seek",@"seconds":@(PorticoChapterStepStart(steps,at))};
}
/** Where Previous or Next chapter goes, by the same rules as client-core's bookChapterMove (a
 * chapter counts as started half a second early; Previous over three seconds in restarts it):
 * `{kind: seek|entry|queue, seconds, entryId}`, or nil for nothing that way. */
static inline NSDictionary *PorticoChapterMove(NSDictionary *plan,double seconds,BOOL next) {
 NSArray *steps=plan[@"steps"];NSInteger first=-1;
 for(NSInteger i=0;i<(NSInteger)steps.count;i++)if(!PorticoChapterStepEntry(steps,i)){first=i;break;}
 if(first<0||!isfinite(seconds))return @{@"kind":@"queue"};
 NSInteger current=first-1;
 for(NSInteger i=first;i<(NSInteger)steps.count&&!PorticoChapterStepEntry(steps,i);i++)if(PorticoChapterStepStart(steps,i)<=seconds+.5)current=i;
 if(next)return PorticoChapterGo(steps,current+1,plan[@"after"]);
 if(current>=0&&!PorticoChapterStepEntry(steps,current)&&seconds-PorticoChapterStepStart(steps,current)>3)return PorticoChapterGo(steps,current,@"end");
 return PorticoChapterGo(steps,current-1,plan[@"before"]);
}
#endif
#endif
