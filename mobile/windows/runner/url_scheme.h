#ifndef RUNNER_URL_SCHEME_H_
#define RUNNER_URL_SCHEME_H_

// Registers covey:// for the current user (#436), pointing at this executable.
//
// The web's pairing card hands a desktop app a covey://pair link (#334); on
// macOS Info.plist claims the scheme, on Windows it is the registry. There is
// no installer (the release is a zip), so the app registers itself when it
// starts. HKEY_CURRENT_USER needs no administrator rights.
//
// Written only when the registration is missing or points at another
// executable: the folder may have been moved, or another copy started last.
// The copy started last wins, which is the one the person just used.
void RegisterUrlScheme();

#endif  // RUNNER_URL_SCHEME_H_
