// Portico bounded PDF page raster worker.
//
// One page per process, one PDFium thread. The parent stages an already
// admitted local publication into this process's stdin, fixes the argv to
// (pageIndex, widthPx), and bounds the raw output. Nothing here reads a path,
// the network, an external file, JavaScript, XFA or document actions.
//
// Output contract (little endian, written after a successful render):
//   bytes  0..3   "PDR1"
//   bytes  4..7   uint32 width
//   bytes  8..11  uint32 height
//   bytes 12..15  uint32 stride
//   bytes 16..19  uint32 format (3 = FPDFBitmap_BGRx)
//   bytes 20..27  uint64 payload bytes
//   bytes 28..31  zero
//   then height * stride bytes, top line first.
//
// Exit 0 success, 3 determined unavailable (not a PDF, encrypted, unsupported
// page/size), 4 retryable/limit failure. Diagnostics go to stderr only.

#include <errno.h>
#include <limits.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <unistd.h>

#include "public/fpdfview.h"

#define MAX_PDF_BYTES (128u * 1024u * 1024u)
#define MAX_WIDTH 2048u
#define MAX_HEIGHT 8192u
#define MAX_PIXELS 8388608u
#define MAX_RAW_BYTES (64u * 1024u * 1024u)
#define HEADER_BYTES 32u

#define EXIT_UNAVAILABLE 3
#define EXIT_RETRY 4

static int limit_process(void) {
  struct rlimit zero;
  memset(&zero, 0, sizeof(zero));
  if (setrlimit(RLIMIT_CORE, &zero) != 0) {
    return 0;
  }
  struct rlimit memory;
  memory.rlim_cur = 1u << 30;
  memory.rlim_max = 1u << 30;
  if (setrlimit(RLIMIT_AS, &memory) != 0) {
    return 0;
  }
  struct rlimit cpu;
  cpu.rlim_cur = 30;
  cpu.rlim_max = 35;
  if (setrlimit(RLIMIT_CPU, &cpu) != 0) {
    return 0;
  }
  return 1;
}

static void fail(const char* why) {
  if (why != NULL && why[0] != '\0') {
    fprintf(stderr, "pdf-raster: %s\n", why);
  }
}

static int read_stdin(uint8_t** out, size_t* out_len) {
  size_t capacity = 1u << 20;
  size_t length = 0;
  uint8_t* buffer = (uint8_t*)malloc(capacity);
  if (buffer == NULL) {
    return 0;
  }
  for (;;) {
    if (length == MAX_PDF_BYTES) {
      // Accept exactly the documented bound. Probe one byte under the parent's
      // deadline: EOF means an exact-size document, any byte is overflow.
      uint8_t extra = 0;
      ssize_t got;
      do {
        got = read(STDIN_FILENO, &extra, 1);
      } while (got < 0 && errno == EINTR);
      if (got != 0) {
        free(buffer);
        return 0;
      }
      break;
    }
    if (length == capacity) {
      size_t next = capacity * 2u;
      if (next > MAX_PDF_BYTES) {
        next = MAX_PDF_BYTES;
      }
      uint8_t* grown = (uint8_t*)realloc(buffer, next);
      if (grown == NULL) {
        free(buffer);
        return 0;
      }
      buffer = grown;
      capacity = next;
    }
    ssize_t got = read(STDIN_FILENO, buffer + length, capacity - length);
    if (got < 0) {
      if (errno == EINTR) {
        continue;
      }
      free(buffer);
      return 0;
    }
    if (got == 0) {
      break;
    }
    length += (size_t)got;
    if (length > MAX_PDF_BYTES) {
      free(buffer);
      return 0;
    }
  }
  if (length == 0) {
    free(buffer);
    return 0;
  }
  *out = buffer;
  *out_len = length;
  return 1;
}

static int parse_uint(const char* raw, unsigned long min, unsigned long max,
                      unsigned long* out) {
  if (raw == NULL || raw[0] == '\0' || raw[0] == '+' || raw[0] == '-') {
    return 0;
  }
  errno = 0;
  char* end = NULL;
  unsigned long value = strtoul(raw, &end, 10);
  if (errno != 0 || end == NULL || *end != '\0' || value < min || value > max) {
    return 0;
  }
  *out = value;
  return 1;
}

static int write_all(const uint8_t* data, size_t length) {
  while (length > 0) {
    ssize_t wrote = write(STDOUT_FILENO, data, length);
    if (wrote < 0) {
      if (errno == EINTR) {
        continue;
      }
      return 0;
    }
    if (wrote == 0) {
      return 0;
    }
    data += (size_t)wrote;
    length -= (size_t)wrote;
  }
  return 1;
}

static void put_u32(uint8_t* out, uint32_t value) {
  out[0] = (uint8_t)(value & 0xffu);
  out[1] = (uint8_t)((value >> 8) & 0xffu);
  out[2] = (uint8_t)((value >> 16) & 0xffu);
  out[3] = (uint8_t)((value >> 24) & 0xffu);
}

static void put_u64(uint8_t* out, uint64_t value) {
  for (int i = 0; i < 8; i++) {
    out[i] = (uint8_t)((value >> (8 * i)) & 0xffu);
  }
}

int main(int argc, char** argv) {
  // Hard worker budgets are installed before any input/parser allocation.
  if (!limit_process()) {
    fail("resource limits are unavailable");
    return EXIT_UNAVAILABLE;
  }
  if (argc != 3) {
    fail("expected page and width");
    return EXIT_UNAVAILABLE;
  }
  unsigned long page_index = 0;
  unsigned long width = 0;
  // The page bound is checked against the real document below; 1,000,000 is a
  // fixed parse bound, never a page count guess.
  if (!parse_uint(argv[1], 0, 1000000, &page_index) ||
      !parse_uint(argv[2], 1, MAX_WIDTH, &width)) {
    fail("invalid page or width");
    return EXIT_UNAVAILABLE;
  }

  uint8_t* pdf = NULL;
  size_t pdf_len = 0;
  if (!read_stdin(&pdf, &pdf_len)) {
    fail("input too large or unreadable");
    return EXIT_RETRY;
  }

  FPDF_LIBRARY_CONFIG config;
  memset(&config, 0, sizeof(config));
  config.version = 2;
  FPDF_InitLibraryWithConfig(&config);

  FPDF_DOCUMENT document =
      FPDF_LoadMemDocument64(pdf, pdf_len, NULL);
  if (document == NULL) {
    unsigned long error = FPDF_GetLastError();
    free(pdf);
    FPDF_DestroyLibrary();
    if (error == FPDF_ERR_PASSWORD || error == FPDF_ERR_FORMAT ||
        error == FPDF_ERR_SECURITY || error == FPDF_ERR_PAGE
#if defined(PDF_ENABLE_XFA)
        || error == FPDF_ERR_XFALOAD || error == FPDF_ERR_XFALAYOUT
#endif
    ) {
      fail("document is not a supported plain PDF");
      return EXIT_UNAVAILABLE;
    }
    fail("document could not be loaded");
    return EXIT_RETRY;
  }

  int page_count = FPDF_GetPageCount(document);
  if (page_count < 1 || page_index >= (unsigned long)page_count) {
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("page is outside the document");
    return EXIT_UNAVAILABLE;
  }

  FPDF_PAGE page = FPDF_LoadPage(document, (int)page_index);
  if (page == NULL) {
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("page could not be loaded");
    return EXIT_UNAVAILABLE;
  }

  double page_width = FPDF_GetPageWidthF(page);
  double page_height = FPDF_GetPageHeightF(page);
  if (!isfinite(page_width) || !isfinite(page_height) || page_width <= 0.0 ||
      page_height <= 0.0) {
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("page has no usable dimensions");
    return EXIT_UNAVAILABLE;
  }

  double scale = (double)width / page_width;
  double scaled_height = ceil(page_height * scale);
  if (!isfinite(scaled_height) || scaled_height < 1.0 ||
      scaled_height > (double)MAX_HEIGHT) {
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("scaled page exceeds the fixed pixel bounds");
    return EXIT_UNAVAILABLE;
  }
  uint64_t height = (uint64_t)scaled_height;
  if (height * (uint64_t)width > (uint64_t)MAX_PIXELS) {
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("page pixel budget exceeded");
    return EXIT_UNAVAILABLE;
  }
  uint64_t stride = ((uint64_t)width * 4u + 3u) & ~(uint64_t)3u;
  if (stride > (uint64_t)INT_MAX || height * stride > (uint64_t)MAX_RAW_BYTES) {
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("page byte budget exceeded");
    return EXIT_UNAVAILABLE;
  }
  size_t pixels = (size_t)(height * stride);
  uint8_t* buffer = (uint8_t*)calloc(pixels, 1);
  if (buffer == NULL) {
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("bitmap allocation failed");
    return EXIT_RETRY;
  }

  FPDF_BITMAP bitmap = FPDFBitmap_CreateEx(
      (int)width, (int)height, FPDFBitmap_BGRx, buffer, (int)stride);
  if (bitmap == NULL) {
    free(buffer);
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("bitmap could not be created");
    return EXIT_RETRY;
  }
  int actual_stride = FPDFBitmap_GetStride(bitmap);
  if (actual_stride != (int)stride) {
    FPDFBitmap_Destroy(bitmap);
    free(buffer);
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("unexpected bitmap stride");
    return EXIT_UNAVAILABLE;
  }
  if (!FPDFBitmap_FillRect(bitmap, 0, 0, (int)width, (int)height, 0xffffffffu)) {
    FPDFBitmap_Destroy(bitmap);
    free(buffer);
    FPDF_ClosePage(page);
    FPDF_CloseDocument(document);
    free(pdf);
    FPDF_DestroyLibrary();
    fail("bitmap could not be initialized");
    return EXIT_RETRY;
  }
  // No annotations, widgets, JavaScript, XFA, actions or external resources.
  FPDF_RenderPageBitmap(bitmap, page, 0, 0, (int)width, (int)height, 0, 0);
  // BGRx leaves one byte per pixel unused; emit a deterministic zero there so
  // no uninitialized byte ever leaves the worker.
  for (size_t i = 0; i < (size_t)width * (size_t)height; i++) {
    buffer[i * 4u + 3u] = 0;
  }

  uint8_t header[HEADER_BYTES];
  memset(header, 0, sizeof(header));
  memcpy(header, "PDR1", 4);
  put_u32(header + 4, (uint32_t)width);
  put_u32(header + 8, (uint32_t)height);
  put_u32(header + 12, (uint32_t)stride);
  put_u32(header + 16, (uint32_t)FPDFBitmap_BGRx);
  put_u64(header + 20, (uint64_t)pixels);

  int ok = write_all(header, sizeof(header)) && write_all(buffer, pixels);

  FPDFBitmap_Destroy(bitmap);
  free(buffer);
  FPDF_ClosePage(page);
  FPDF_CloseDocument(document);
  free(pdf);
  FPDF_DestroyLibrary();
  if (!ok) {
    fail("output could not be written");
    return EXIT_RETRY;
  }
  return 0;
}
