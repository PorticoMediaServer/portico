cmake_minimum_required(VERSION 3.24)
project(portico_mobi_worker C)
# This is the only target: upstream mobidrm/mobimeta tools are never built.
# build.py verifies the complete pinned clean source checkout before configure.
if(NOT DEFINED LIBMOBI_SOURCE OR NOT DEFINED MUSL_PREFIX OR NOT DEFINED PORTICO_LLD)
  message(FATAL_ERROR "Verified libmobi source and private musl prefix required")
endif()
set(CMAKE_C_STANDARD 99)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(mobi_sources buffer compression debug index memory meta parse_rawml read structure util write opf xmlwriter)
set(source_paths)
foreach(name IN LISTS mobi_sources)
  list(APPEND source_paths "${LIBMOBI_SOURCE}/src/${name}.c")
endforeach()
# A single complete bundled miniz TU supplies inflate AND EPUB ZIP functions;
# do not use upstream's duplicate-definition linker workaround.
list(APPEND source_paths "${LIBMOBI_SOURCE}/src/miniz.c" "${LIBMOBI_SOURCE}/tools/common.c")
add_library(mobi_cli OBJECT "${LIBMOBI_SOURCE}/tools/mobitool.c")
target_compile_definitions(mobi_cli PRIVATE main=portico_mobitool_main)
add_executable(portico-mobi-worker worker.c ${source_paths} $<TARGET_OBJECTS:mobi_cli>)
foreach(target IN ITEMS mobi_cli portico-mobi-worker)
  target_include_directories(${target} PRIVATE "${LIBMOBI_SOURCE}/src" "${LIBMOBI_SOURCE}/tools")
  target_compile_definitions(${target} PRIVATE USE_XMLWRITER USE_MINIZ MINIZ_NO_ZLIB_COMPATIBLE_NAMES _POSIX_C_SOURCE=200809L PACKAGE_VERSION="0.12")
  target_compile_options(${target} PRIVATE -include "${CMAKE_CURRENT_SOURCE_DIR}/build-policy.h" -nostdinc -isystem "${MUSL_PREFIX}/include" -O2 -fstack-protector-strong -fno-ident -ffile-prefix-map=${LIBMOBI_SOURCE}=/source/libmobi -ffile-prefix-map=${CMAKE_CURRENT_SOURCE_DIR}=/source/portico-mobi)
endforeach()
# Explicit complete static closure. No compiler libgcc/host libc search fallback.
target_link_options(portico-mobi-worker PRIVATE -static -nostdlib -fuse-ld=${PORTICO_LLD} "${MUSL_PREFIX}/lib/crt1.o" "${MUSL_PREFIX}/lib/crti.o" -Wl,--no-undefined -Wl,-z,relro,-z,now,-z,noexecstack -Wl,--build-id=none)
target_link_libraries(portico-mobi-worker PRIVATE "${MUSL_PREFIX}/lib/libc.a" "${MUSL_PREFIX}/lib/crtn.o")
