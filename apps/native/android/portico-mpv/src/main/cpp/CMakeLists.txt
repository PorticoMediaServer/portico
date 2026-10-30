cmake_minimum_required(VERSION 3.22.1)
project(porticompv C)

# The media engine's libmpv for this architecture and its headers (see ../../../build.gradle and
# third_party/media-engine/build-android.sh). The bridge links libmpv only; libmpv loads FFmpeg,
# libass, libplacebo and the rest itself.
set(PORTICO_LIBMPV "${PORTICO_LIBMPV_DIR}/lib/${ANDROID_ABI}/libmpv.so")
if(NOT EXISTS "${PORTICO_LIBMPV}" OR NOT EXISTS "${PORTICO_LIBMPV_DIR}/include/mpv/client.h")
  message(FATAL_ERROR "The media engine is not built for ${ANDROID_ABI} in ${PORTICO_LIBMPV_DIR}. Run third_party/media-engine/build-android.sh, or pass -PporticoLibmpvDir.")
endif()
add_library(mpv SHARED IMPORTED)
set_target_properties(mpv PROPERTIES IMPORTED_LOCATION "${PORTICO_LIBMPV}")

add_library(porticompv SHARED portico_mpv_jni.c)
set_target_properties(porticompv PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED ON)
target_include_directories(porticompv PRIVATE "${PORTICO_LIBMPV_DIR}/include")
target_link_libraries(porticompv PRIVATE mpv)
