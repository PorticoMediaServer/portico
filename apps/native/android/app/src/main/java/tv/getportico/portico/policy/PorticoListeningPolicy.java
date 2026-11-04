package tv.getportico.portico.policy;

import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

/** Pure projection of the canonical listening policy. Saved music rate and
 * autoplay-next no longer exist; queue postPlay owns automatic continuation. */
public final class PorticoListeningPolicy {
  private PorticoListeningPolicy() {}
  public static boolean validPreferences(Object value, Set<Double> bookRates, Set<Double> passoutMinutes) {
    if (value == null) return true;
    if (!(value instanceof Map<?, ?> preferences) || preferences.size() != 2) return false;
    return member(preferences.get("bookRate"), bookRates) && member(preferences.get("passoutMinutes"), passoutMinutes);
  }
  private static boolean member(Object value, Set<Double> values) {
    return value instanceof Number number && Double.isFinite(number.doubleValue()) && values.contains(number.doubleValue());
  }
  public static boolean validAudiobookPreferences(Object value) {
    if (!(value instanceof Map<?, ?> book) || book.size() != 3) return false;
    return member(book.get("skipBackMs"), Set.of(10000.0,15000.0,30000.0,60000.0))
        && member(book.get("skipForwardMs"), Set.of(15000.0,30000.0,60000.0,90000.0))
        && member(book.get("resumeRewindMs"), Set.of(0.0,5000.0,15000.0,30000.0));
  }
  public static double skipSeconds(boolean audiobook, Object value, boolean forward) {
    if (!audiobook) return 30;
    if (!(value instanceof Map<?, ?> book)) return forward ? 30 : 15;
    return ((Number)book.get(forward ? "skipForwardMs" : "skipBackMs")).doubleValue()/1000;
  }
  public static double resumeDisplacement(double elapsed, double position, double choiceMs) {
    if (!Double.isFinite(elapsed) || elapsed < 300 || !Double.isFinite(position) || position <= 0
        || !(choiceMs==0 || choiceMs==5000 || choiceMs==15000 || choiceMs==30000)) return 0;
    return Math.min(position, choiceMs/1000);
  }
  public static boolean mayAdvance(boolean timerStopped, Map<?, ?> postPlay) {
    return !timerStopped && (postPlay == null || !Boolean.FALSE.equals(postPlay.get("autoplay")));
  }
  public static Double preferredRate(boolean audiobook, Object value) {
    if (!audiobook) return 1.0;
    if (!(value instanceof Map<?, ?> preferences) || !(preferences.get("bookRate") instanceof Number number)) return null;
    return Double.isFinite(number.doubleValue()) ? number.doubleValue() : null;
  }
  public static boolean rateAllowed(boolean audiobook, Double rate, Set<Double> bookRates) {
    return rate != null && Double.isFinite(rate) && (audiobook ? bookRates.contains(rate) : rate == 1.0);
  }

  /** Reconcile a same-session server read without letting a replan rewind an observed clock. */
  public static final class SessionUpdate {
    public final String intent;
    public final boolean patchWanted;
    public final Double seekSeconds;
    public final boolean dropPrepared;
    private SessionUpdate(String intent, boolean patchWanted, Double seekSeconds, boolean dropPrepared) {
      this.intent = intent; this.patchWanted = patchWanted; this.seekSeconds = seekSeconds; this.dropPrepared = dropPrepared;
    }
  }
  public static SessionUpdate reconcileSession(Map<?, ?> before, Map<?, ?> after, String intent,
      boolean keepLocalIntent, Double observedSeconds, Double pendingSeekSeconds) {
    if (!(before.get("id") instanceof String) || !Objects.equals(before.get("id"), after.get("id"))
        || !Objects.equals(before.get("itemId"), after.get("itemId"))
        || !(after.get("revision") instanceof String revision) || revision.isEmpty()
        || !(before.get("presentation") instanceof Map<?, ?> old)
        || !(after.get("presentation") instanceof Map<?, ?> fresh)) return null;
    Double oldGeneration = nonnegative(old.get("generation")), generation = nonnegative(fresh.get("generation"));
    Double oldStart = nonnegative(old.get("startPositionMs")), start = nonnegative(fresh.get("startPositionMs"));
    if (oldGeneration == null || generation == null || generation < oldGeneration
        || generation != Math.floor(generation) || generation > 9007199254740991.0 || oldStart == null || start == null) return null;
    String state = after.get("state") instanceof String value ? value : "";
    if (!(state.equals("playing") || state.equals("paused")) || !(intent.equals("playing") || intent.equals("paused"))) return null;
    String nextIntent = keepLocalIntent ? intent : state;
    boolean sourceChanged = generation > oldGeneration || !Objects.equals(old.get("url"), fresh.get("url"));
    // A changed server start is a seek, including direct play where the generation is unchanged.
    boolean serverSeek = !start.equals(oldStart);
    Double seek = null;
    if (serverSeek) seek = start / 1000;
    else if (sourceChanged) {
      if (nonnegative(pendingSeekSeconds) != null) seek = pendingSeekSeconds;
      else if (nonnegative(observedSeconds) != null) seek = observedSeconds;
      else seek = start / 1000;
    }
    return new SessionUpdate(nextIntent, keepLocalIntent && !nextIntent.equals(state), seek,
        sourceChanged || serverSeek || !nextIntent.equals(intent));
  }
  private static Double nonnegative(Object value) {
    if (!(value instanceof Number number)) return null;
    double n = number.doubleValue();
    return Double.isFinite(n) && n >= 0 ? n : null;
  }
  /** A book part's chapter steps for the lock screen's Previous and Next (MU-7): the shape
   * client-core's bookChapterSteps sends, `{sessionId, itemId, steps, before, after}`. */
  public static boolean validChapterSteps(Object value) {
    if (!(value instanceof Map<?, ?> plan) || plan.size() != 5 || !wireId(plan.get("sessionId")) || !wireId(plan.get("itemId"))
        || !PAST.contains(plan.get("before")) || !PAST.contains(plan.get("after"))
        || !(plan.get("steps") instanceof List<?> steps) || steps.isEmpty() || steps.size() > 4100) return false;
    int run = 0; // 0 before the part's own steps, 1 in them, 2 after them
    for (Object raw : steps) {
      if (!(raw instanceof Map<?, ?> step) || step.size() != 2 || !step.containsKey("entryId")) return false;
      Double start = nonnegative(step.get("startSeconds"));
      Object entry = step.get("entryId");
      if (start == null || start > 1e9 || entry != null && !wireId(entry)) return false;
      if (entry == null) { if (run == 2) return false; run = 1; } else if (run == 1) run = 2;
    }
    return run > 0;
  }
  private static final Set<String> PAST = Set.of("end", "queue");
  private static boolean wireId(Object value) { return value instanceof String text && text.matches("[A-Za-z0-9_-]{1,128}"); }

  /** Where Previous or Next chapter goes: a seek in this part, another part's entry from a
   * chapter start, or the queue's own move. Null: nothing that way. */
  public static final class ChapterMove {
    public final String kind;
    public final double seconds;
    public final String entryId;
    private ChapterMove(String kind, double seconds, String entryId) { this.kind = kind; this.seconds = seconds; this.entryId = entryId; }
  }
  private static final ChapterMove QUEUE_MOVE = new ChapterMove("queue", 0, null);
  /** The same rules as client-core's bookChapterMove: a chapter counts as started half a second
   * early, and Previous more than three seconds in restarts it. */
  public static ChapterMove chapterMove(Map<?, ?> plan, double seconds, boolean next) {
    List<?> steps = (List<?>) plan.get("steps");
    int first = -1;
    for (int i = 0; i < steps.size(); i++) if (stepEntry(steps, i) == null) { first = i; break; }
    if (first < 0 || !Double.isFinite(seconds)) return QUEUE_MOVE;
    int current = first - 1;
    for (int i = first; i < steps.size() && stepEntry(steps, i) == null; i++) if (stepStart(steps, i) <= seconds + 0.5) current = i;
    if (next) return chapterGo(steps, current + 1, plan.get("after"));
    if (current >= 0 && stepEntry(steps, current) == null && seconds - stepStart(steps, current) > 3) return chapterGo(steps, current, "end");
    return chapterGo(steps, current - 1, plan.get("before"));
  }
  private static ChapterMove chapterGo(List<?> steps, int at, Object past) {
    if (at < 0 || at >= steps.size()) return "queue".equals(past) ? QUEUE_MOVE : null;
    String entry = stepEntry(steps, at);
    return new ChapterMove(entry == null ? "seek" : "entry", stepStart(steps, at), entry);
  }
  private static String stepEntry(List<?> steps, int at) { return (String) ((Map<?, ?>) steps.get(at)).get("entryId"); }
  private static double stepStart(List<?> steps, int at) { return ((Number) ((Map<?, ?>) steps.get(at)).get("startSeconds")).doubleValue(); }

  /** Safety holds (lease, interruption, timer) do not imply a failed media engine. */
  public static boolean resumeNeedsRetry(boolean ended, boolean engineFailed) { return ended || engineFailed; }
}
