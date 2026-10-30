// Server-local inference of the original ISC musicnn MTT model. This helper
// receives bounded numeric features, never media paths, network URLs or keys.
#include <onnxruntime_cxx_api.h>
#include <array>
#include <cmath>
#include <cstdint>
#include <cwchar>
#include <cstring>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <string>
#include <vector>

namespace {
static_assert(sizeof(float) == 4 && std::numeric_limits<float>::is_iec559, "float32 protocol requires IEEE-754");
constexpr size_t kFrames = 187, kMels = 96, kEmbedding = 200, kTags = 50;
constexpr size_t kPatch = kFrames * kMels;
constexpr char kVersion[] = "516acb2a0ff5ef73f64547898e018e793152c506-onnx1";

bool read_patch(std::array<float, kPatch>& patch) {
  std::array<unsigned char, kPatch * 4> bytes{};
  std::cin.read(reinterpret_cast<char*>(bytes.data()), bytes.size());
  const auto count = std::cin.gcount();
  if (count == 0 && std::cin.eof()) return false;
  if (count != static_cast<std::streamsize>(bytes.size())) throw std::runtime_error("partial feature patch");
  for (size_t i = 0; i < patch.size(); ++i) {
    const auto* p = bytes.data() + i * 4;
    const uint32_t bits = uint32_t(p[0]) | uint32_t(p[1]) << 8 | uint32_t(p[2]) << 16 | uint32_t(p[3]) << 24;
    std::memcpy(&patch[i], &bits, 4);
    if (!std::isfinite(patch[i]) || patch[i] < 0 || patch[i] > 16) throw std::runtime_error("invalid log-mel feature");
  }
  return true;
}

void write_float(float value) {
  uint32_t bits;
  std::memcpy(&bits, &value, 4);
  const std::array<char, 4> bytes = {char(bits), char(bits >> 8), char(bits >> 16), char(bits >> 24)};
  std::cout.write(bytes.data(), bytes.size());
}

void metadata(const Ort::ModelMetadata& model, const char* key, const char* expected,
              Ort::AllocatorWithDefaultOptions& allocator) {
  const auto value = model.LookupCustomMetadataMapAllocated(key, allocator);
  if (!value || std::strcmp(value.get(), expected)) throw std::runtime_error("unverified model metadata");
}

void infer(const ORTCHAR_T* model_path) {
  if (std::strcmp(OrtGetApiBase()->GetVersionString(), "1.20.1")) throw std::runtime_error("different ONNX Runtime version");
  Ort::Env env(ORT_LOGGING_LEVEL_ERROR, "portico-sonic");
  Ort::SessionOptions options;
  options.SetIntraOpNumThreads(1);
  options.SetInterOpNumThreads(1);
  options.SetExecutionMode(ExecutionMode::ORT_SEQUENTIAL);
  options.DisableCpuMemArena();
  options.DisableMemPattern();
  Ort::Session session(env, model_path, options);
  Ort::AllocatorWithDefaultOptions allocator;
  const auto model = session.GetModelMetadata();
  metadata(model, "portico.model.id", "musicnn-mtt", allocator);
  metadata(model, "portico.model.version", kVersion, allocator);
  metadata(model, "portico.model.licence", "ISC", allocator);
  metadata(model, "portico.model.source", "516acb2a0ff5ef73f64547898e018e793152c506", allocator);
  metadata(model, "portico.preprocessing", "musicnn-logmel-1", allocator);
  if (session.GetInputCount() != 1 || session.GetOutputCount() != 2) throw std::runtime_error("different model boundary");
  const auto input_name = session.GetInputNameAllocated(0, allocator);
  const auto embedding_name = session.GetOutputNameAllocated(0, allocator);
  const auto tags_name = session.GetOutputNameAllocated(1, allocator);
  if (std::strcmp(input_name.get(), "log_mel") || std::strcmp(embedding_name.get(), "embedding") ||
      std::strcmp(tags_name.get(), "tags")) throw std::runtime_error("different model tensors");
  const char* inputs[] = {"log_mel"};
  const char* outputs[] = {"embedding", "tags"};
  const std::array<int64_t, 3> shape = {1, kFrames, kMels};
  auto memory = Ort::MemoryInfo::CreateCpu(OrtArenaAllocator, OrtMemTypeDefault);
  std::array<float, kPatch> patch{};
  std::cout.write("PSNC0001", 8);
  std::cout.flush();
  size_t patches = 0;
  while (read_patch(patch)) {
    if (++patches > 65536) throw std::runtime_error("feature budget exceeded");
    auto tensor = Ort::Value::CreateTensor<float>(memory, patch.data(), patch.size(), shape.data(), shape.size());
    auto result = session.Run(Ort::RunOptions{nullptr}, inputs, &tensor, 1, outputs, 2);
    for (size_t output = 0; output < 2; ++output) {
      const size_t dimensions = output == 0 ? kEmbedding : kTags;
      if (!result[output].IsTensor()) throw std::runtime_error("non-tensor result");
      const auto info = result[output].GetTensorTypeAndShapeInfo();
      if (info.GetElementType() != ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT ||
          info.GetShape() != std::vector<int64_t>({1, static_cast<int64_t>(dimensions)})) throw std::runtime_error("different output dimensions");
      const float* values = result[output].GetTensorData<float>();
      double squared = 0;
      for (size_t i = 0; i < dimensions; ++i) {
        const float value = values[i];
        if (!std::isfinite(value) || (output == 1 && (value < 0 || value > 1))) throw std::runtime_error("invalid inference result");
        squared += double(value) * double(value);
      }
      if (output == 0 && squared == 0) throw std::runtime_error("empty embedding");
      for (size_t i = 0; i < dimensions; ++i) write_float(values[i]);
    }
    std::cout.flush();
    if (!std::cout) throw std::runtime_error("result pipe closed");
  }
}
}  // namespace

#ifdef _WIN32
int wmain(int argc, wchar_t** argv) {
  const bool valid = argc == 3 && std::wcscmp(argv[1], L"--model") == 0;
#else
int main(int argc, char** argv) {
  const bool valid = argc == 3 && std::strcmp(argv[1], "--model") == 0;
#endif
  try {
    if (!valid) throw std::runtime_error("expected one verified model path");
    infer(argv[2]);
    return 0;
  } catch (const std::exception& error) {
    std::cerr << "Sonic unavailable: " << error.what() << '\n';
    return 1;
  }
}
