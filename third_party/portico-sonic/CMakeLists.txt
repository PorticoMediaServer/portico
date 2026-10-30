cmake_minimum_required(VERSION 3.20)
project(portico_sonic LANGUAGES CXX)

# The runtime is a separately licensed, verified deployment dependency. Never
# fetch it during configuration or silently select a system/global install.
if(NOT ORT_ROOT)
  message(FATAL_ERROR "ORT_ROOT must name the verified ONNX Runtime 1.20.1 CPU package")
endif()
find_path(ORT_INCLUDE onnxruntime_cxx_api.h PATHS "${ORT_ROOT}/include" NO_DEFAULT_PATH REQUIRED)
find_library(ORT_LIBRARY NAMES onnxruntime PATHS "${ORT_ROOT}/lib" NO_DEFAULT_PATH REQUIRED)
add_executable(portico-sonic main.cpp)
target_compile_features(portico-sonic PRIVATE cxx_std_17)
target_include_directories(portico-sonic PRIVATE "${ORT_INCLUDE}")
target_link_libraries(portico-sonic PRIVATE "${ORT_LIBRARY}")
if(MSVC)
  target_compile_options(portico-sonic PRIVATE /W4 /fp:strict)
else()
  target_compile_options(portico-sonic PRIVATE -Wall -Wextra -Werror -fno-fast-math -ffp-contract=off)
endif()
# No -march=native or AVX2 minimum. ONNX Runtime's CPU dispatch chooses the
# available implementation. Actual loading/inference, including old CPUs, is
# checked out of process; an unavailable runtime never crashes the Go server.
if(APPLE)
  set_target_properties(portico-sonic PROPERTIES INSTALL_RPATH "@loader_path")
elseif(UNIX)
  set_target_properties(portico-sonic PROPERTIES INSTALL_RPATH "$ORIGIN/lib")
endif()
install(TARGETS portico-sonic RUNTIME DESTINATION bin)
