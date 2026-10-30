#import <Foundation/Foundation.h>
#import <math.h>

/** Publish only the controller's actual external-presentation acknowledgement. A ready
 * playback fact or a matching requested binding alone does not establish readiness. */
static id PorticoExternalPresentationBindingFact(NSDictionary *ready, NSDictionary *current, NSString *lease) {
 if(![ready isKindOfClass:NSDictionary.class]||![current isKindOfClass:NSDictionary.class]||
    ![lease isKindOfClass:NSString.class]||!lease.length||![ready isEqual:current]||![ready[@"lease"] isEqual:lease])return NSNull.null;
 for(NSString *key in @[@"lease",@"sessionId",@"itemId"]){
  if(![ready[key] isKindOfClass:NSString.class]||![ready[key] length])return NSNull.null;
 }
 for(NSString *key in @[@"intentId",@"generation"]){
  id value=ready[key];
  if(![value isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)value)==CFBooleanGetTypeID())return NSNull.null;
  double number=[value doubleValue];
  if(!isfinite(number)||number<1||number>9007199254740991.0||floor(number)!=number)return NSNull.null;
 }
 // Copy the flat tuple rather than publishing mutable command data or extra fields.
 return @{@"lease":[ready[@"lease"] copy],@"intentId":ready[@"intentId"],
          @"sessionId":[ready[@"sessionId"] copy],@"generation":ready[@"generation"],
          @"itemId":[ready[@"itemId"] copy]};
}
