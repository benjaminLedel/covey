#include "url_scheme.h"

#include <windows.h>

#include <string>

namespace {

constexpr const wchar_t kSchemeKey[] = L"Software\\Classes\\covey";
constexpr const wchar_t kCommandKey[] =
    L"Software\\Classes\\covey\\shell\\open\\command";
constexpr const wchar_t kDescription[] = L"URL:covey";

// The full path of the running executable; empty on failure. The buffer grows
// past MAX_PATH, since a folder under a long user profile path can exceed it.
std::wstring ExecutablePath() {
  std::wstring path(MAX_PATH, L'\0');
  while (path.size() <= 32768) {
    DWORD length = ::GetModuleFileNameW(nullptr, path.data(),
                                        static_cast<DWORD>(path.size()));
    if (length == 0) {
      return std::wstring();
    }
    if (length < path.size()) {
      path.resize(length);
      return path;
    }
    path.resize(path.size() * 2);
  }
  return std::wstring();
}

// Reads a string value under HKEY_CURRENT_USER; name nullptr is the key's
// default value. False when the key or the value is absent.
bool ReadString(const wchar_t* key, const wchar_t* name, std::wstring* value) {
  DWORD size = 0;
  if (::RegGetValueW(HKEY_CURRENT_USER, key, name, RRF_RT_REG_SZ, nullptr,
                     nullptr, &size) != ERROR_SUCCESS) {
    return false;
  }
  std::wstring buffer(size / sizeof(wchar_t) + 1, L'\0');
  size = static_cast<DWORD>(buffer.size() * sizeof(wchar_t));
  if (::RegGetValueW(HKEY_CURRENT_USER, key, name, RRF_RT_REG_SZ, nullptr,
                     buffer.data(), &size) != ERROR_SUCCESS) {
    return false;
  }
  buffer.resize(buffer.find(L'\0'));
  *value = buffer;
  return true;
}

// Writes a string value under HKEY_CURRENT_USER, creating the key if needed.
bool WriteString(const wchar_t* key, const wchar_t* name,
                 const std::wstring& value) {
  const DWORD bytes =
      static_cast<DWORD>((value.size() + 1) * sizeof(wchar_t));
  return ::RegSetKeyValueW(HKEY_CURRENT_USER, key, name, REG_SZ, value.c_str(),
                           bytes) == ERROR_SUCCESS;
}

}  // namespace

void RegisterUrlScheme() {
  const std::wstring executable = ExecutablePath();
  if (executable.empty()) {
    return;
  }
  // "%1" quoted: Windows passes the whole link as one argument, and the
  // app_links plugin reads exactly one argument after the program name.
  const std::wstring command = L"\"" + executable + L"\" \"%1\"";

  std::wstring description;
  std::wstring protocol;
  std::wstring current;
  if (ReadString(kSchemeKey, nullptr, &description) &&
      description == kDescription &&
      ReadString(kSchemeKey, L"URL Protocol", &protocol) &&
      ReadString(kCommandKey, nullptr, &current) && current == command) {
    return;
  }

  // A failure is not reported: the app starts either way, and only the link
  // from the web goes unanswered.
  WriteString(kSchemeKey, nullptr, kDescription);
  WriteString(kSchemeKey, L"URL Protocol", L"");
  WriteString(kCommandKey, nullptr, command);
}
