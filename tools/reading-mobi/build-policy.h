/* Force-included into EVERY libmobi/common/CLI/worker translation unit. */
#if !defined(USE_XMLWRITER)
#error EPUB writer must be enabled
#endif
#if defined(USE_ENCRYPTION) || defined(USE_LIBXML2) || defined(USE_ZLIB) || defined(HAVE_CONFIG_H)
#error External XML/zlib, generated config, and encryption are forbidden in this bundle
#endif
#if defined(MOBI_DEBUG) || defined(MOBI_DEBUG_ALLOC)
#error Parser diagnostic/debug configurations are not deliverable
#endif
