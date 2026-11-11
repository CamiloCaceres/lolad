#ifndef LOLAD_MACOS_H
#define LOLAD_MACOS_H

// Login item (SMAppService.mainApp, macOS 13+). Status: 0 not registered,
// 1 enabled, 2 needs approval in System Settings, 3 not found (not a bundle),
// -1 unsupported OS.
int lolad_login_status(void);
// Returns 0 on success; otherwise a malloc'd message in *err (free it).
int lolad_login_set(int on, char **err);
void lolad_open_login_settings(void);

// Dock icon only while a window is open: 1 = regular app, 0 = accessory.
void lolad_set_dock_visible(int visible);

// 1 when the process was started as a login item (open event carried 'lgit').
int lolad_launched_at_login(void);

// Moves path to the Trash. Returns 0 on success; else a malloc'd message in *err.
int lolad_trash(const char *path, char **err);

#endif
