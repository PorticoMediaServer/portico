/*
 * The bridge between Kotlin and libmpv's client API for the mpv player engine.
 *
 * It is deliberately thin: one mpv handle per "player", every call a direct libmpv call, and one
 * thread per handle that waits for mpv's events and hands each to Kotlin with the few fields the
 * engine reads (a command's reply token and result, a file's playlist entry id, why a file ended).
 *
 * Text crosses the boundary as UTF-8 bytes, never as JNI "modified UTF-8" strings: a file name or
 * a track title may hold any Unicode character (or bytes that are not valid text at all), and the
 * JNI string functions abort on some of those. Option, property and command names are plain ASCII
 * and are passed as strings.
 */
#include <jni.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <mpv/client.h>

/*
 * FFmpeg's MediaCodec decoders and mpv's Android outputs reach Java through a JavaVM that the
 * application must hand over. A libmpv with FFmpeg linked in statically exports the setter under
 * mpv's own name; a libmpv beside shared FFmpeg libraries leaves FFmpeg's own name visible.
 * Whichever this build has is used.
 */
extern int mpv_lavc_set_java_vm(void *vm) __attribute__((weak));
extern int av_jni_set_java_vm(void *vm, void *log_ctx) __attribute__((weak));

typedef struct portico_mpv {
    mpv_handle *mpv;
    JavaVM *vm;
    jobject listener;
    jmethodID on_event;
    jmethodID on_property;
    jmethodID on_log;
    /* The surface mpv holds as its window (a global reference), or NULL. */
    jobject surface;
    pthread_t thread;
    int thread_started;
    atomic_int quit;
} portico_mpv;

static portico_mpv *from(jlong handle) {
    return (portico_mpv *)(intptr_t)handle;
}

/* A NUL-terminated copy of a Java byte array. The caller frees it. NULL when out of memory. */
static char *copy_bytes(JNIEnv *env, jbyteArray array) {
    if (!array)
        return NULL;
    jsize length = (*env)->GetArrayLength(env, array);
    char *out = malloc((size_t)length + 1);
    if (!out)
        return NULL;
    if (length > 0)
        (*env)->GetByteArrayRegion(env, array, 0, length, (jbyte *)out);
    out[length] = 0;
    return out;
}

static jbyteArray to_bytes(JNIEnv *env, const char *text) {
    if (!text)
        return NULL;
    size_t length = strlen(text);
    if (length > (size_t)INT32_MAX)
        return NULL;
    jbyteArray array = (*env)->NewByteArray(env, (jsize)length);
    if (array && length > 0)
        (*env)->SetByteArrayRegion(env, array, 0, (jsize)length, (const jbyte *)text);
    return array;
}

static void clear_exception(JNIEnv *env) {
    if ((*env)->ExceptionCheck(env)) {
        (*env)->ExceptionDescribe(env);
        (*env)->ExceptionClear(env);
    }
}

static void *event_loop(void *argument) {
    portico_mpv *p = argument;
    JNIEnv *env = NULL;
    JavaVMAttachArgs attach = {.version = JNI_VERSION_1_6, .name = "portico-mpv-events", .group = NULL};
    if ((*p->vm)->AttachCurrentThread(p->vm, &env, &attach) != JNI_OK || !env)
        return NULL;

    while (!atomic_load(&p->quit)) {
        mpv_event *event = mpv_wait_event(p->mpv, -1);
        if (atomic_load(&p->quit))
            break;
        if (!event || event->event_id == MPV_EVENT_NONE)
            continue;

        jlong userdata = 0;
        jlong entry = 0;
        jint reason = 0;
        jint error = (jint)event->error;

        switch (event->event_id) {
        case MPV_EVENT_LOG_MESSAGE: {
            mpv_event_log_message *message = event->data;
            if (message && message->prefix && message->level && message->text) {
                jbyteArray prefix = to_bytes(env, message->prefix);
                jbyteArray level = to_bytes(env, message->level);
                jbyteArray text = to_bytes(env, message->text);
                if (prefix && level && text)
                    (*env)->CallVoidMethod(env, p->listener, p->on_log, prefix, level, text);
                clear_exception(env);
                if (prefix) (*env)->DeleteLocalRef(env, prefix);
                if (level) (*env)->DeleteLocalRef(env, level);
                if (text) (*env)->DeleteLocalRef(env, text);
            }
            continue;
        }
        case MPV_EVENT_PROPERTY_CHANGE: {
            mpv_event_property *property = event->data;
            if (property && property->name) {
                jstring name = (*env)->NewStringUTF(env, property->name);
                if (name)
                    (*env)->CallVoidMethod(env, p->listener, p->on_property, name);
                clear_exception(env);
                if (name) (*env)->DeleteLocalRef(env, name);
            }
            continue;
        }
        case MPV_EVENT_START_FILE: {
            mpv_event_start_file *start = event->data;
            if (start)
                entry = (jlong)start->playlist_entry_id;
            break;
        }
        case MPV_EVENT_END_FILE: {
            mpv_event_end_file *end = event->data;
            if (end) {
                entry = (jlong)end->playlist_entry_id;
                reason = (jint)end->reason;
                error = (jint)end->error;
            }
            break;
        }
        case MPV_EVENT_COMMAND_REPLY:
            userdata = (jlong)event->reply_userdata;
            break;
        default:
            break;
        }

        (*env)->CallVoidMethod(env, p->listener, p->on_event, (jint)event->event_id, userdata, error, entry, reason);
        clear_exception(env);

        /* After shutdown the handle may only be destroyed: nothing more is read from it. */
        if (event->event_id == MPV_EVENT_SHUTDOWN)
            break;
    }

    (*p->vm)->DetachCurrentThread(p->vm);
    return NULL;
}

#define JNI(type, name) JNIEXPORT type JNICALL Java_tv_getportico_portico_mpv_PorticoMpvNative_##name

/* Creates an mpv handle that is not yet initialised. Returns 0 when mpv cannot be created. */
JNI(jlong, create)(JNIEnv *env, jclass type, jobject listener) {
    (void)type;
    if (!listener)
        return 0;
    portico_mpv *p = calloc(1, sizeof(*p));
    if (!p)
        return 0;
    atomic_init(&p->quit, 0);
    if ((*env)->GetJavaVM(env, &p->vm) != JNI_OK || !p->vm) {
        free(p);
        return 0;
    }
    jclass listener_class = (*env)->GetObjectClass(env, listener);
    p->on_event = (*env)->GetMethodID(env, listener_class, "onEvent", "(IJIJI)V");
    p->on_property = (*env)->GetMethodID(env, listener_class, "onProperty", "(Ljava/lang/String;)V");
    p->on_log = (*env)->GetMethodID(env, listener_class, "onLog", "([B[B[B)V");
    (*env)->DeleteLocalRef(env, listener_class);
    if (!p->on_event || !p->on_property || !p->on_log) {
        (*env)->ExceptionClear(env);
        free(p);
        return 0;
    }
    if (mpv_lavc_set_java_vm)
        mpv_lavc_set_java_vm(p->vm);
    else if (av_jni_set_java_vm)
        av_jni_set_java_vm(p->vm, NULL);

    p->mpv = mpv_create();
    if (!p->mpv) {
        free(p);
        return 0;
    }
    p->listener = (*env)->NewGlobalRef(env, listener);
    if (!p->listener) {
        mpv_terminate_destroy(p->mpv);
        free(p);
        return 0;
    }
    mpv_request_log_messages(p->mpv, "warn");
    return (jlong)(intptr_t)p;
}

/* Sets an option before or after initialisation. Returns mpv's result (0, or a negative error). */
JNI(jint, setOption)(JNIEnv *env, jclass type, jlong handle, jstring name, jbyteArray value) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name || !value)
        return MPV_ERROR_INVALID_PARAMETER;
    const char *option = (*env)->GetStringUTFChars(env, name, NULL);
    char *text = copy_bytes(env, value);
    jint result = MPV_ERROR_NOMEM;
    if (option && text)
        result = mpv_set_option_string(p->mpv, option, text);
    if (option) (*env)->ReleaseStringUTFChars(env, name, option);
    free(text);
    return result;
}

/* Initialises mpv and starts delivering its events. Returns 0, or a negative mpv error. */
JNI(jint, initialize)(JNIEnv *env, jclass type, jlong handle) {
    (void)env;
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv)
        return MPV_ERROR_INVALID_PARAMETER;
    if (p->thread_started)
        return 0;
    int result = mpv_initialize(p->mpv);
    if (result < 0)
        return result;
    if (pthread_create(&p->thread, NULL, event_loop, p) != 0)
        return MPV_ERROR_NOMEM;
    p->thread_started = 1;
    return 0;
}

/* Asks for a change notice for a property (the value is read separately). */
JNI(jint, observe)(JNIEnv *env, jclass type, jlong handle, jstring name) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name)
        return MPV_ERROR_INVALID_PARAMETER;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    jint result = property ? mpv_observe_property(p->mpv, 0, property, MPV_FORMAT_NONE) : MPV_ERROR_NOMEM;
    if (property) (*env)->ReleaseStringUTFChars(env, name, property);
    return result;
}

#define MAX_ARGUMENTS 32

/* Collects command arguments. Returns the count, or -1; `out` is NULL-terminated on success. */
static int collect_arguments(JNIEnv *env, jobjectArray arguments, char **out) {
    if (!arguments)
        return -1;
    jsize count = (*env)->GetArrayLength(env, arguments);
    if (count < 1 || count >= MAX_ARGUMENTS)
        return -1;
    for (jsize i = 0; i < count; i++) {
        jbyteArray item = (jbyteArray)(*env)->GetObjectArrayElement(env, arguments, i);
        out[i] = copy_bytes(env, item);
        if (item) (*env)->DeleteLocalRef(env, item);
        if (!out[i]) {
            for (jsize j = 0; j < i; j++) free(out[j]);
            return -1;
        }
    }
    out[count] = NULL;
    return (int)count;
}

static void free_arguments(char **arguments, int count) {
    for (int i = 0; i < count; i++) free(arguments[i]);
}

/* Runs a command and waits for it. Returns mpv's result. */
JNI(jint, command)(JNIEnv *env, jclass type, jlong handle, jobjectArray arguments) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv)
        return MPV_ERROR_INVALID_PARAMETER;
    char *list[MAX_ARGUMENTS];
    int count = collect_arguments(env, arguments, list);
    if (count < 0)
        return MPV_ERROR_INVALID_PARAMETER;
    jint result = mpv_command(p->mpv, (const char **)list);
    free_arguments(list, count);
    return result;
}

/* Starts a command; its completion arrives as a command-reply event carrying `token`. */
JNI(jint, commandAsync)(JNIEnv *env, jclass type, jlong handle, jlong token, jobjectArray arguments) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv)
        return MPV_ERROR_INVALID_PARAMETER;
    char *list[MAX_ARGUMENTS];
    int count = collect_arguments(env, arguments, list);
    if (count < 0)
        return MPV_ERROR_INVALID_PARAMETER;
    jint result = mpv_command_async(p->mpv, (uint64_t)token, (const char **)list);
    free_arguments(list, count);
    return result;
}

JNI(jint, setProperty)(JNIEnv *env, jclass type, jlong handle, jstring name, jbyteArray value) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name || !value)
        return MPV_ERROR_INVALID_PARAMETER;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    char *text = copy_bytes(env, value);
    jint result = MPV_ERROR_NOMEM;
    if (property && text)
        result = mpv_set_property_string(p->mpv, property, text);
    if (property) (*env)->ReleaseStringUTFChars(env, name, property);
    free(text);
    return result;
}

/* A property as mpv prints it (lists and maps as JSON), in UTF-8 bytes; null when unavailable. */
JNI(jbyteArray, getString)(JNIEnv *env, jclass type, jlong handle, jstring name) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name)
        return NULL;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    if (!property)
        return NULL;
    char *text = mpv_get_property_string(p->mpv, property);
    (*env)->ReleaseStringUTFChars(env, name, property);
    if (!text)
        return NULL;
    jbyteArray out = to_bytes(env, text);
    mpv_free(text);
    return out;
}

/* A number property; NaN when unavailable. */
JNI(jdouble, getDouble)(JNIEnv *env, jclass type, jlong handle, jstring name) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name)
        return (jdouble)NAN;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    if (!property)
        return (jdouble)NAN;
    double value = 0;
    int result = mpv_get_property(p->mpv, property, MPV_FORMAT_DOUBLE, &value);
    (*env)->ReleaseStringUTFChars(env, name, property);
    return result >= 0 ? (jdouble)value : (jdouble)NAN;
}

/* A yes/no property: 1, 0, or -1 when unavailable. */
JNI(jint, getFlag)(JNIEnv *env, jclass type, jlong handle, jstring name) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name)
        return -1;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    if (!property)
        return -1;
    int value = 0;
    int result = mpv_get_property(p->mpv, property, MPV_FORMAT_FLAG, &value);
    (*env)->ReleaseStringUTFChars(env, name, property);
    return result >= 0 ? (value ? 1 : 0) : -1;
}

/* A whole-number property; INT64_MIN when unavailable. */
JNI(jlong, getLong)(JNIEnv *env, jclass type, jlong handle, jstring name) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv || !name)
        return (jlong)INT64_MIN;
    const char *property = (*env)->GetStringUTFChars(env, name, NULL);
    if (!property)
        return (jlong)INT64_MIN;
    int64_t value = 0;
    int result = mpv_get_property(p->mpv, property, MPV_FORMAT_INT64, &value);
    (*env)->ReleaseStringUTFChars(env, name, property);
    return result >= 0 ? (jlong)value : (jlong)INT64_MIN;
}

/*
 * Gives mpv the surface to draw on (its "wid"), or takes it away with null. mpv keeps the
 * reference for as long as a video output is open on it, so the caller closes the video output
 * (vo=null) before taking a surface away, and opens one only after giving a surface.
 */
JNI(jint, setSurface)(JNIEnv *env, jclass type, jlong handle, jobject surface) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p || !p->mpv)
        return MPV_ERROR_INVALID_PARAMETER;
    jobject next = surface ? (*env)->NewGlobalRef(env, surface) : NULL;
    if (surface && !next)
        return MPV_ERROR_NOMEM;
    int64_t wid = (int64_t)(intptr_t)next;
    int result = mpv_set_option(p->mpv, "wid", MPV_FORMAT_INT64, &wid);
    if (result < 0) {
        if (next) (*env)->DeleteGlobalRef(env, next);
        return result;
    }
    if (p->surface) (*env)->DeleteGlobalRef(env, p->surface);
    p->surface = next;
    return 0;
}

JNI(jstring, errorString)(JNIEnv *env, jclass type, jint code) {
    (void)type;
    const char *text = mpv_error_string(code);
    return (*env)->NewStringUTF(env, text ? text : "");
}

/* Ends mpv and frees the handle. Nothing may use the handle afterwards. May block briefly. */
JNI(void, destroy)(JNIEnv *env, jclass type, jlong handle) {
    (void)type;
    portico_mpv *p = from(handle);
    if (!p)
        return;
    atomic_store(&p->quit, 1);
    if (p->mpv)
        mpv_wakeup(p->mpv);
    if (p->thread_started) {
        pthread_join(p->thread, NULL);
        p->thread_started = 0;
    }
    if (p->mpv) {
        mpv_terminate_destroy(p->mpv);
        p->mpv = NULL;
    }
    if (p->surface) (*env)->DeleteGlobalRef(env, p->surface);
    if (p->listener) (*env)->DeleteGlobalRef(env, p->listener);
    free(p);
}
