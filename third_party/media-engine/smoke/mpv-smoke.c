/*
 * A small libmpv program for checking a media engine build on a device or emulator, without the
 * app: it loads libmpv (and so every library it needs), plays one file to the end with no video
 * or audio output, and prints what it found. Built and run by android-smoke.sh.
 *
 *   mpv-smoke <file or URL> [option=value ...]
 *
 * Prints "version ...", "track ..." lines, every subtitle text it sees ("sub-text ...") and mpv's
 * log at warning level (MPV_SMOKE_LOG=v for more), then "result eof" (exit 0) or "result error <message>" (exit 1).
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <mpv/client.h>

static void print_property(mpv_handle *mpv, const char *label, const char *name)
{
    char *value = mpv_get_property_string(mpv, name);
    printf("%s %s\n", label, value ? value : "(none)");
    mpv_free(value);
}

int main(int argc, char **argv)
{
    if (argc < 2) {
        fprintf(stderr, "usage: mpv-smoke <file> [option=value ...]\n");
        return 2;
    }
    mpv_handle *mpv = mpv_create();
    if (!mpv) {
        printf("result error mpv_create failed\n");
        return 1;
    }
    const char *defaults[][2] = {
        {"config", "no"}, {"terminal", "no"}, {"vo", "null"}, {"ao", "null"}, {"untimed", "yes"},
        /* idle=yes as in the app: with "no", mpv would quit before loadfile arrives. */
        {"idle", "yes"}, {"sub-auto", "no"}, {"hwdec", "no"}, {"tls-verify", "yes"},
    };
    for (size_t i = 0; i < sizeof(defaults) / sizeof(defaults[0]); i++)
        mpv_set_option_string(mpv, defaults[i][0], defaults[i][1]);
    for (int i = 2; i < argc; i++) {
        char *pair = strdup(argv[i]);
        char *eq = strchr(pair, '=');
        if (!eq) {
            printf("result error bad option %s\n", argv[i]);
            return 2;
        }
        *eq = 0;
        int code = mpv_set_option_string(mpv, pair, eq + 1);
        if (code < 0) {
            printf("result error option %s: %s\n", pair, mpv_error_string(code));
            return 1;
        }
        free(pair);
    }
    /* MPV_SMOKE_LOG=v (or debug) shows more of mpv's log when a case needs investigating. */
    const char *level = getenv("MPV_SMOKE_LOG");
    mpv_request_log_messages(mpv, level && *level ? level : "warn");
    if (mpv_initialize(mpv) < 0) {
        printf("result error mpv_initialize failed\n");
        return 1;
    }
    print_property(mpv, "version", "mpv-version");
    print_property(mpv, "ffmpeg", "ffmpeg-version");
    mpv_observe_property(mpv, 0, "sub-text", MPV_FORMAT_STRING);

    const char *command[] = {"loadfile", argv[1], NULL};
    if (mpv_command(mpv, command) < 0) {
        printf("result error loadfile was refused\n");
        return 1;
    }
    int status = 1, restarted = 0;
    for (;;) {
        mpv_event *event = mpv_wait_event(mpv, 60);
        if (event->event_id == MPV_EVENT_NONE) {
            printf("result error timed out\n");
            break;
        }
        if (event->event_id == MPV_EVENT_SHUTDOWN) {
            printf("result error mpv shut down\n");
            break;
        }
        if (event->event_id == MPV_EVENT_LOG_MESSAGE) {
            mpv_event_log_message *log = event->data;
            printf("log %s %s: %s", log->level, log->prefix, log->text);
        } else if (event->event_id == MPV_EVENT_FILE_LOADED) {
            print_property(mpv, "tracks", "track-list");
            print_property(mpv, "format", "file-format");
            print_property(mpv, "video", "video-codec");
            print_property(mpv, "audio", "audio-codec");
        } else if (event->event_id == MPV_EVENT_PLAYBACK_RESTART && !restarted) {
            /* The decoders are open now: the track list names them. */
            restarted = 1;
            print_property(mpv, "playing", "track-list");
        } else if (event->event_id == MPV_EVENT_PROPERTY_CHANGE) {
            mpv_event_property *property = event->data;
            if (property->format == MPV_FORMAT_STRING && *(char **)property->data && **(char **)property->data)
                printf("sub-text %s\n", *(char **)property->data);
        } else if (event->event_id == MPV_EVENT_END_FILE) {
            mpv_event_end_file *end = event->data;
            if (end->reason == MPV_END_FILE_REASON_EOF) {
                printf("result eof\n");
                status = 0;
            } else {
                printf("result error %s\n", mpv_error_string(end->error));
            }
            break;
        }
    }
    /* Log lines can arrive just after the end of the file: print those too. */
    for (;;) {
        mpv_event *event = mpv_wait_event(mpv, 0.5);
        if (event->event_id == MPV_EVENT_NONE)
            break;
        if (event->event_id == MPV_EVENT_LOG_MESSAGE) {
            mpv_event_log_message *log = event->data;
            printf("log %s %s: %s", log->level, log->prefix, log->text);
        }
    }
    mpv_terminate_destroy(mpv);
    return status;
}
